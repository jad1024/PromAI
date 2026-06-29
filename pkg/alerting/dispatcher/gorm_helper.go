package dispatcher

import "gorm.io/gorm"

// gormExpr 包装 gorm.Expr，方便单元测试 mock
func gormExpr(sql string, args ...interface{}) interface{} {
	return gorm.Expr(sql, args...)
}
