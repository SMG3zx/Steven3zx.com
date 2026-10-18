package accounting

type Event struct {
	Level  string
	Name   string
	Fields map[string]string
}

type Flush struct{}

type Flushed struct{ Err error }
