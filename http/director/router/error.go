package router


type Error struct {
	msg string
	status int
}

func NewError(status int, msg string) *Error {
	return &Error{msg: msg, status: status}
}

func (e *Error) Error() string {
	return e.msg
}

func (e *Error) StatusCode() int {
	return e.status
}