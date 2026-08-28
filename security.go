package main

import (
	"log"
	"os"

	"PromAI/pkg/crypto"
)

// storeKey 返回数据加密密钥：优先 PROMAI_STORE_KEY，其次全局 JWT secret。
func storeKey() string {
	if k := os.Getenv("PROMAI_STORE_KEY"); k != "" {
		return k
	}
	return jwtSecretGlobal
}

// encryptSecret 加密敏感字段；空串或已是 enc: 密文时原样返回（防止双重加密）。
func encryptSecret(s string) string {
	if s == "" || crypto.IsEncrypted(s) {
		return s
	}
	enc, err := crypto.EncryptSecret(s, storeKey())
	if err != nil {
		log.Printf("[Crypto] 加密失败: %v", err)
		return s
	}
	return enc
}

// decryptSecret 解密敏感字段；无 enc: 前缀的历史明文原样返回。
func decryptSecret(s string) string {
	if !crypto.IsEncrypted(s) {
		return s
	}
	dec, err := crypto.DecryptSecret(s, storeKey())
	if err != nil {
		log.Printf("[Crypto] 解密失败: %v", err)
		return s
	}
	return dec
}
