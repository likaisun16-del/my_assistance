package repo

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type ProjectModel struct {
	ID           int64 `gorm:"primaryKey"`
	UserID       int64 `gorm:"not null;index"`
	Title        string `gorm:"size:255;not null"`
	BrandVoiceID *int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (ProjectModel) TableName() string { return "projects" }

type ProjectRepo struct {
	db *gorm.DB
}

func NewProjectRepo(db *gorm.DB) *ProjectRepo { return &ProjectRepo{db: db} }

func (r *ProjectRepo) Create(ctx context.Context, userID int64, title string) (int64, error) {
	row := &ProjectModel{UserID: userID, Title: title}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

func (r *ProjectRepo) Get(ctx context.Context, id int64) (*ProjectModel, error) {
	var p ProjectModel
	if err := r.db.WithContext(ctx).First(&p, id).Error; err != nil {
		return nil, err
	}
	return &p, nil
}
