package data

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func NewPG(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{})
}
