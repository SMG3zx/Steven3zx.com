package authentication

type HashPassword struct{ Password string }

type PasswordHashed struct {
	Encoded string
	Err     error
}

type VerifyPassword struct {
	Password string
	Encoded  string
}

type PasswordVerified struct {
	Valid bool
	Err   error
}

type IssueToken struct{ Subject string }

type TokenIssued struct {
	Token     string
	ExpiresAt int64
	Err       error
}

type ValidateToken struct{ Token string }

type TokenValidated struct {
	Subject string
	Valid   bool
	Err     error
}
