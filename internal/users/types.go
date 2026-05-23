package users

import "time"

const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	BootstrapUserID = "user-server"
)

type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Token struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

type TokenIssue struct {
	Token  Token  `json:"token"`
	Secret string `json:"secret"`
}

type Principal struct {
	User User
}

type CreateUserInput struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
	Password    string `json:"password"`
	Role        string `json:"role,omitempty"`
}

type UpdateUserInput struct {
	DisplayName *string `json:"displayName,omitempty"`
	Password    *string `json:"password,omitempty"`
}

type CreateTokenInput struct {
	Label string `json:"label,omitempty"`
}

type LoginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	User      User   `json:"user"`
	Token     string `json:"token"`
	TokenMeta Token  `json:"tokenMeta"`
}
