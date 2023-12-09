package kucoin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/rasteiro11/PogCore/pkg/logger"
)

type requestConfig struct {
	payload any
	header  http.Header
	client  *http.Client
}

type RequestOption func(*requestConfig)

func WithHTTPClient(client *http.Client) RequestOption {
	return func(rc *requestConfig) {
		rc.client = client
	}
}

func WithPayload(payload any) RequestOption {
	return func(o *requestConfig) {
		o.payload = payload
	}
}

func WithToken(token string) RequestOption {
	return func(o *requestConfig) {
		o.header.Add("Authorization", "Bearer "+token)
	}
}

func WithHeader(key string, value string) RequestOption {
	return func(rc *requestConfig) {
		rc.header.Add(key, value)
	}
}

func newRequestOption(opts ...RequestOption) *requestConfig {
	options := &requestConfig{
		header: make(http.Header),
	}

	for _, opt := range opts {
		opt(options)
	}

	return options
}

func NewRequest[T any](ctx context.Context, url, method string, opts ...RequestOption) (T, error) {
	var target T

	options := newRequestOption(opts...)

	var (
		token string
		err   error
	)

	var payload io.Reader
	if options.payload != nil && options.header.Get("Content-Type") == "" {
		if body, err := json.Marshal(options.payload); err == nil {
			payload = bytes.NewBuffer(body)
			logger.Of(ctx).Debugf("package=kucoin body=%s\n", string(body))
		}
	} else {
		if options.payload != nil {
			if r, ok := options.payload.(io.Reader); ok {
				payload = r
			}
		}
	}

	logger.Of(ctx).Debugf("package=kucoin method=%s url=%s", method, url)

	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		logger.Of(ctx).Errorf("package=kucoin call=http.NewRequestWithContext() error=%+v\n", err)
		return target, err
	}

	req.Header = options.header

	if v := req.Header.Get("Content-Type"); v == "" {
		req.Header.Add("Content-Type", "application/json")
	}

	req.Header.Add("api-version", "1.0")

	if token != "" {
		req.Header.Add("Authorization", "Bearer "+token)
	}

	res, err := options.client.Do(req)
	if err != nil {
		logger.Of(ctx).Errorf("package=kucoin call=http.DefaultClient.Do() error=%+v\n", err)
		return target, err
	}

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		logger.Of(ctx).Errorf("package=kucoin call=http.DefaultClient.Do() error=%+v\n", err)
		return target, err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return target, &Error{
			Code: res.StatusCode,
			Body: string(body),
		}
	}

	if res.ContentLength != 0 {
		if err := json.Unmarshal(body, &target); err != nil {
			logger.Of(ctx).Errorf("package=kucoin call=json.Unmarshal() error=%+v\n", err)
			return target, err
		}
	}

	return target, nil
}
