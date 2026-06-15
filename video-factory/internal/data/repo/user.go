package repo

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/AGI-Core/AGI-saber/video-factory/internal/biz"
)

type UserModel struct {
	ID           int64     `gorm:"primaryKey"`
	Phone        string    `gorm:"uniqueIndex;size:20;not null"`
	Nickname     string    `gorm:"size:64"`
	PasswordHash string    `gorm:"size:128;not null"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (UserModel) TableName() string { return "users" }

type UserRepo struct {
	db *gorm.DB
}

func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) Create(ctx context.Context, u *biz.User) error {
	row := &UserModel{Phone: u.Phone, Nickname: u.Nickname, PasswordHash: u.PasswordHash}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		// PG unique violation
		if isUniqueViolation(err) {
			return biz.ErrPhoneExists
		}
		return err
	}
	u.ID = row.ID
	return nil
}

func (r *UserRepo) FindByPhone(ctx context.Context, phone string) (*biz.User, error) {
	var row UserModel
	if err := r.db.WithContext(ctx).Where("phone = ?", phone).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrUserNotFound
		}
		return nil, err
	}
	return &biz.User{
		ID:           row.ID,
		Phone:        row.Phone,
		Nickname:     row.Nickname,
		PasswordHash: row.PasswordHash,
	}, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// pgx 错误信息一定含 "duplicate key value violates unique constraint"
	for i := 0; i < len(msg)-15; i++ {
		if msg[i:i+15] == "duplicate key v" {
			return true
		}
	}
	return false
}
