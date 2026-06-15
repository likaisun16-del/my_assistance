package biz

import (
	"context"
	"testing"
)

type fakeUserRepo struct {
	users map[string]*User
	id    int64
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{users: map[string]*User{}}
}

func (f *fakeUserRepo) Create(_ context.Context, u *User) error {
	if _, ok := f.users[u.Phone]; ok {
		return ErrPhoneExists
	}
	f.id++
	u.ID = f.id
	f.users[u.Phone] = u
	return nil
}

func (f *fakeUserRepo) FindByPhone(_ context.Context, p string) (*User, error) {
	if u, ok := f.users[p]; ok {
		return u, nil
	}
	return nil, ErrUserNotFound
}

func TestRegister_HashesPasswordAndStores(t *testing.T) {
	uc := NewUserUsecase(newFakeUserRepo())
	u, err := uc.Register(context.Background(), "13800000000", "p@ss1234", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if u.PasswordHash == "p@ss1234" {
		t.Fatal("password should be hashed")
	}
	if u.Phone != "13800000000" {
		t.Fatalf("phone mismatch: %s", u.Phone)
	}
	if u.ID == 0 {
		t.Fatal("id should be set")
	}
}

func TestRegister_DuplicatePhoneRejected(t *testing.T) {
	uc := NewUserUsecase(newFakeUserRepo())
	if _, err := uc.Register(context.Background(), "13800000000", "p@ss1234", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.Register(context.Background(), "13800000000", "p@ss1234", "b"); err != ErrPhoneExists {
		t.Fatalf("expect ErrPhoneExists, got %v", err)
	}
}

func TestLogin_OK(t *testing.T) {
	uc := NewUserUsecase(newFakeUserRepo())
	if _, err := uc.Register(context.Background(), "13800000001", "hello123", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.Login(context.Background(), "13800000001", "hello123"); err != nil {
		t.Fatalf("expect login ok, got %v", err)
	}
}

func TestLogin_BadPassword(t *testing.T) {
	uc := NewUserUsecase(newFakeUserRepo())
	if _, err := uc.Register(context.Background(), "13800000002", "hello123", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.Login(context.Background(), "13800000002", "wrong"); err != ErrBadPassword {
		t.Fatalf("expect ErrBadPassword, got %v", err)
	}
}
