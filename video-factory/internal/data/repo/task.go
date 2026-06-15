package repo

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type TaskModel struct {
	ID        int64          `gorm:"primaryKey"`
	ProjectID int64          `gorm:"not null;index"`
	Type      string         `gorm:"size:32;not null"`
	Status    string         `gorm:"size:16;not null;default:pending"`
	Params    datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"`
	Result    datatypes.JSON `gorm:"type:jsonb"`
	Error     string         `gorm:"type:text"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (TaskModel) TableName() string { return "tasks" }

type TaskRepo struct {
	db *gorm.DB
}

func NewTaskRepo(db *gorm.DB) *TaskRepo { return &TaskRepo{db: db} }

func (r *TaskRepo) Create(ctx context.Context, projectID int64, taskType string, params any) (int64, error) {
	b, err := json.Marshal(params)
	if err != nil {
		return 0, err
	}
	row := &TaskModel{
		ProjectID: projectID,
		Type:      taskType,
		Status:    "running",
		Params:    b,
	}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

func (r *TaskRepo) Get(ctx context.Context, id int64) (*TaskModel, error) {
	var t TaskModel
	if err := r.db.WithContext(ctx).First(&t, id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TaskRepo) UpdateResult(ctx context.Context, id int64, status string, result any, errMsg string) error {
	updates := map[string]any{
		"status": status,
		"error":  errMsg,
	}
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		updates["result"] = datatypes.JSON(b)
	}
	return r.db.WithContext(ctx).Model(&TaskModel{}).Where("id = ?", id).Updates(updates).Error
}

func (r *TaskRepo) GetResultJSON(ctx context.Context, id int64) (json.RawMessage, string, error) {
	var t TaskModel
	if err := r.db.WithContext(ctx).Select("status", "result").First(&t, id).Error; err != nil {
		return nil, "", err
	}
	return json.RawMessage(t.Result), t.Status, nil
}
