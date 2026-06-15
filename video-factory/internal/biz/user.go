package biz

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrPhoneExists  = errors.New("phone already exists")
	ErrUserNotFound = errors.New("user not found")
	ErrBadPassword  = errors.New("bad password")
)

type User struct {
	ID           int64
	Phone        string
	Nickname     string
	PasswordHash string
}

type UserRepo interface {
	Create(ctx context.Context, u *User) error
	FindByPhone(ctx context.Context, phone string) (*User, error)
}

type UserUsecase struct {
	repo UserRepo
}

func NewUserUsecase(r UserRepo) *UserUsecase {
	return &UserUsecase{repo: r}
}

func (uc *UserUsecase) Register(ctx context.Context, phone, password, nickname string) (*User, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &User{Phone: phone, Nickname: nickname, PasswordHash: string(h)}
	if err := uc.repo.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (uc *UserUsecase) Login(ctx context.Context, phone, password string) (*User, error) {
	u, err := uc.repo.FindByPhone(ctx, phone)
	if err != nil {
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrBadPassword
	}
	return u, nil
}
