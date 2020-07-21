package forward

import (
	"net/http"
)

// ResponseWriterWrapper StatusCodeを後から参照できるようにする
type ResponseWriterWrapper struct {
	http.ResponseWriter
	StatusCode int
}

// WriteHeader httpステータスコードを設定
func (w *ResponseWriterWrapper) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
	w.StatusCode = code
}

// NewResponseWriterWrapper ベースとなるResponseWriterをResponseWriterWrapperでwrapしたものを返す
func NewResponseWriterWrapper(w http.ResponseWriter) *ResponseWriterWrapper {
	return &ResponseWriterWrapper{
		ResponseWriter: w,
		StatusCode:     http.StatusOK,
	}
}
