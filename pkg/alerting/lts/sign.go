package lts

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// signHWSRequest 对请求做华为云 SDK-HMAC-SHA256 签名（AK/SK 认证）。
//
// 与 sync 包内 CES 用的签名一致，但补齐了规范请求的第 6 行（请求体摘要），
// 以支持带 JSON body 的 POST（LTS 查询日志接口为 POST）。
//
// 签名流程：
//  1. CanonicalRequest = Method \n CanonicalURI \n CanonicalQueryString \n
//     CanonicalHeaders \n SignedHeaders \n HexEncode(SHA256(RequestPayload))
//  2. StringToSign = "SDK-HMAC-SHA256" \n X-Sdk-Date \n HexEncode(SHA256(CanonicalRequest))
//  3. Signature = HexEncode(HMAC-SHA256(SecretKey, StringToSign))
//  4. Authorization: SDK-HMAC-SHA256 Access=<ak>, SignedHeaders=<...>, Signature=<...>
func signHWSRequest(req *http.Request, ak, sk string) error {
	now := time.Now().UTC()
	xSdkDate := now.Format("20060102T150405Z")
	req.Header.Set("X-Sdk-Date", xSdkDate)

	// 读取请求体用于计算摘要（读取后回填，保证 body 可重复消费）
	var payload []byte
	if req.Body != nil && req.Body != http.NoBody {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return fmt.Errorf("读取请求体失败: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(b))
		payload = b
	}
	payloadHash := sha256HexBytes(payload)

	// 参与签名的 headers：host + x-sdk-date（+ content-type，当有 body 时）
	headers := map[string]string{
		"host":       req.URL.Host,
		"x-sdk-date": xSdkDate,
	}
	if len(payload) > 0 {
		ct := req.Header.Get("Content-Type")
		if strings.TrimSpace(ct) == "" {
			ct = "application/json;charset=UTF-8"
			req.Header.Set("Content-Type", ct)
		}
		headers["content-type"] = ct
	}

	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var canonicalHeaders strings.Builder
	for _, k := range keys {
		canonicalHeaders.WriteString(k + ":" + strings.TrimSpace(headers[k]) + "\n")
	}
	signedHeaders := strings.Join(keys, ";")

	// CanonicalURI：RFC 3986 编码路径，计算签名时需以 "/" 结尾（发送时可不带）
	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	if !strings.HasSuffix(canonicalURI, "/") {
		canonicalURI += "/"
	}

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQueryString(req.URL.RawQuery),
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	stringToSign := "SDK-HMAC-SHA256\n" + xSdkDate + "\n" + sha256HexString(canonicalRequest)

	mac := hmac.New(sha256.New, []byte(sk))
	mac.Write([]byte(stringToSign))
	signature := hex.EncodeToString(mac.Sum(nil))

	auth := fmt.Sprintf("SDK-HMAC-SHA256 Access=%s, SignedHeaders=%s, Signature=%s", ak, signedHeaders, signature)
	req.Header.Set("Authorization", auth)
	return nil
}

// canonicalQueryString 对 query 参数按参数名升序、URI 编码后拼接。
func canonicalQueryString(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	params := strings.Split(rawQuery, "&")
	keys := make([]string, 0, len(params))
	pairs := make(map[string]string, len(params))
	for _, p := range params {
		if p == "" {
			continue
		}
		var k, v string
		if idx := strings.Index(p, "="); idx >= 0 {
			k, v = p[:idx], p[idx+1:]
		} else {
			k = p
		}
		keys = append(keys, k)
		pairs[k] = v
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+pairs[k])
	}
	return strings.Join(parts, "&")
}

func sha256HexBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func sha256HexString(s string) string {
	return sha256HexBytes([]byte(s))
}
