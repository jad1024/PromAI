// Package crypto 提供敏感数据的统一 AES-256-GCM 加密封装。
//
// 设计要点：
//   - 所有密文以 "enc:" 前缀标记，解密时对无前缀的旧明文做向后兼容（返回原文）。
//   - 存储密钥优先取环境变量 PROMAI_STORE_KEY；未设置时回退到 JWT secret
//     （与历史版本 piagent.EncryptAPIKey 的密钥体系保持一致，避免存量密文失效）。
//   - 密钥经 SHA-256 派生为 32 字节 AES-256 密钥。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"os"
	"strings"
)

// EncPrefix 密文前缀，标记该值已加密。
const EncPrefix = "enc:"

// IsEncrypted 判断字符串是否为加密值。
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, EncPrefix)
}

// deriveKey 将任意长度密钥派生为 32 字节 AES-256 密钥。
func deriveKey(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// StoreKey 返回数据加密密钥：优先 PROMAI_STORE_KEY，其次 PROMAI_JWT_SECRET。
// 两个环境变量都为空时返回空串，由调用方决定是否报错。
func StoreKey() string {
	if k := os.Getenv("PROMAI_STORE_KEY"); k != "" {
		return k
	}
	return os.Getenv("PROMAI_JWT_SECRET")
}

// EncryptSecret 加密明文，返回带 enc: 前缀的 base64 密文。
func EncryptSecret(plaintext, key string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(deriveKey(key))
	if err != nil {
		log.Printf("[Crypto] 创建加密块失败: %v", err)
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		log.Printf("[Crypto] 创建 GCM 失败: %v", err)
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		log.Printf("[Crypto] 生成 nonce 失败: %v", err)
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return EncPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptSecret 解密带 enc: 前缀的密文；无前缀的字符串视为历史明文，原样返回。
func DecryptSecret(encoded, key string) (string, error) {
	if !IsEncrypted(encoded) {
		return encoded, nil
	}
	raw := strings.TrimPrefix(encoded, EncPrefix)
	block, err := aes.NewCipher(deriveKey(key))
	if err != nil {
		log.Printf("[Crypto] 创建解密块失败: %v", err)
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		log.Printf("[Crypto] 创建 GCM 失败: %v", err)
		return "", err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		log.Printf("[Crypto] Base64 解码失败: %v", err)
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		log.Printf("[Crypto] 解密失败: %v", err)
		return "", err
	}
	return string(plaintext), nil
}
