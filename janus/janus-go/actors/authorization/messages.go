package authorization

type Check struct {
	Subject  string
	Action   string
	Resource map[string]any
}

type Decision struct {
	Allowed bool
	Err     error
}
