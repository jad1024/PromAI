package piagent

import (
	"PromAI/pkg/crypto"
)

// resolveKey 加密密钥解析：优先环境变量 PROMAI_STORE_KEY，其次调用方传入的 JWT secret，
// 保证与 database 层数据加密密钥一致（防止设置 PROMAI_STORE_KEY 后两边密钥不同步）。
func resolveKey(jwtSecret string) string {
	if k := crypto.StoreKey(); k != "" {
		return k
	}
	return jwtSecret
}

// EncryptAPIKey 加密 API Key，返回带 enc: 前缀的密文。
func EncryptAPIKey(plaintext, jwtSecret string) (string, error) {
	return crypto.EncryptSecret(plaintext, resolveKey(jwtSecret))
}

// DecryptAPIKey 解密 API Key；无 enc: 前缀的历史明文原样返回。
func DecryptAPIKey(encoded, jwtSecret string) (string, error) {
	return crypto.DecryptSecret(encoded, resolveKey(jwtSecret))
}
