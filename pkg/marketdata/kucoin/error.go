package kucoin

import "fmt"

type Error struct {
	Code int    `json:"status"`
	Body string `json:"body"`
}

func (e *Error) Error() string {
	return fmt.Sprintf(`"status":"%d", "body": "%s"`, e.Code, e.Body)
}
