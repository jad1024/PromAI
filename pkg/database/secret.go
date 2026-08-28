package database

import (
	"log"
	"sync/atomic"

	"PromAI/pkg/crypto"
)

// storeKey 全局数据加密密钥，由 main 包在启动时通过 SetStoreKey 注入
// （来源：PROMAI_STORE_KEY 或 JWT secret），保证与 HTTP 层加解密一致。
var storeKey atomic.Value // string

// SetStoreKey 设置全局数据加密密钥（在初始化 DB 后、任何读写前调用）。
func SetStoreKey(key string) {
	storeKey.Store(key)
}

// getStoreKey 获取加密密钥：优先显式注入的密钥，其次环境变量。
func getStoreKey() string {
	if k, ok := storeKey.Load().(string); ok && k != "" {
		return k
	}
	return crypto.StoreKey()
}

// encryptField 加密敏感字段；空串或已是密文直接返回。
func encryptField(s string) string {
	if s == "" || crypto.IsEncrypted(s) {
		return s
	}
	enc, err := crypto.EncryptSecret(s, getStoreKey())
	if err != nil {
		// 加密失败时降级返回原文（打日志便于排查），避免写入流程中断
		return s
	}
	return enc
}

// decryptField 解密敏感字段；无 enc: 前缀的历史明文原样返回。
func decryptField(s string) string {
	if !crypto.IsEncrypted(s) {
		return s
	}
	dec, err := crypto.DecryptSecret(s, getStoreKey())
	if err != nil {
		return s
	}
	return dec
}

// MigrateSecrets 启动时一次性迁移存量明文凭据为加密存储。
// 在 InitDB 之后、任何业务读写之前调用；对已加密记录幂等跳过。
func MigrateSecrets() error {
	var dss []DataSource
	if err := DB.Find(&dss).Error; err != nil {
		return err
	}
	migrated := 0
	for _, ds := range dss {
		if ds.Password != "" && !crypto.IsEncrypted(ds.Password) {
			if err := DB.Model(&DataSource{}).Where("id = ?", ds.ID).
				Update("password", encryptField(ds.Password)).Error; err != nil {
				return err
			}
			migrated++
		}
	}
	var ext []ExternalAlertSource
	if err := DB.Find(&ext).Error; err != nil {
		return err
	}
	for _, e := range ext {
		updates := map[string]any{}
		if e.SecretKey != "" && !crypto.IsEncrypted(e.SecretKey) {
			updates["secret_key"] = encryptField(e.SecretKey)
		}
		if e.Password != "" && !crypto.IsEncrypted(e.Password) {
			updates["password"] = encryptField(e.Password)
		}
		if e.N9eToken != "" && !crypto.IsEncrypted(e.N9eToken) {
			updates["n9e_token"] = encryptField(e.N9eToken)
		}
		if e.Token != "" && !crypto.IsEncrypted(e.Token) {
			updates["token"] = encryptField(e.Token)
		}
		if len(updates) > 0 {
			if err := DB.Model(&ExternalAlertSource{}).Where("id = ?", e.ID).Updates(updates).Error; err != nil {
				return err
			}
			migrated++
		}
	}
	if migrated > 0 {
		log.Printf("[Crypto] 已完成 %d 条存量凭据的加密迁移", migrated)
	}
	// 通知渠道 ConfigJSON 敏感字段迁移（幂等）
	var chs []NotificationChannel
	if err := DB.Find(&chs).Error; err != nil {
		return err
	}
	chMigrated := 0
	for _, ch := range chs {
		enc := EncryptNotifyConfigJSON(ch.ChannelType, ch.ConfigJSON)
		if enc != ch.ConfigJSON {
			if err := DB.Model(&NotificationChannel{}).Where("id = ?", ch.ID).
				Update("config_json", enc).Error; err != nil {
				return err
			}
			chMigrated++
		}
	}
	if chMigrated > 0 {
		log.Printf("[Crypto] 已完成 %d 条通知渠道配置的加密迁移", chMigrated)
	}
	return nil
}
