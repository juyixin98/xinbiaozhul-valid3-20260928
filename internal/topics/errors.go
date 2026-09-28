package topics

import "fmt"

type topicError struct{ msg string }

func (e *topicError) Error() string { return e.msg }

func errf(format string, args ...any) error {
	return &topicError{msg: fmt.Sprintf(format, args...)}
}

// IsTopicError reports whether err is a topic validation error.
func IsTopicError(err error) bool {
	_, ok := err.(*topicError)
	return ok
}
