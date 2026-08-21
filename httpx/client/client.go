// Package client là HTTP client cho việc gọi ra ngoài, có retry, circuit breaker,
// propagate trace và log đã che dữ liệu nhạy cảm.
//
// Vì sao không dùng http.Client thuần: bốn thứ trên là những thứ mọi service đều
// cần khi gọi ra ngoài, và nếu để mỗi chỗ tự viết thì mỗi chỗ sẽ thiếu một cái khác
// nhau — thường là thiếu breaker, và hậu quả là service chết theo dịch vụ mà nó gọi.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cqt002/gokit/core/errs"
	"github.com/cqt002/gokit/core/log"
	"github.com/cqt002/gokit/core/retry"
	"github.com/cqt002/gokit/core/tlsx"
	"github.com/cqt002/gokit/core/tracectx"
)

// Giá trị mặc định của Config.
const (
	DefaultTimeout      = 10 * time.Second
	DefaultTotalTimeout = 30 * time.Second
	DefaultMaxIdleConns = 100
)

// Config cấu hình Client.
type Config struct {
	// BaseURL là tiền tố cho mọi đường dẫn tương đối.
	BaseURL string

	// Timeout là thời gian tối đa cho **mỗi lần thử**. <= 0 thì dùng DefaultTimeout.
	Timeout time.Duration

	// TotalTimeout là thời gian tối đa cho toàn bộ lời gọi, tính cả các lần retry.
	// <= 0 thì dùng DefaultTotalTimeout.
	//
	// Phải có cả hai: chỉ có timeout mỗi lần thử thì 5 lần retry mỗi lần 10 giây
	// thành 50 giây, vượt xa deadline của request đang gọi nó.
	TotalTimeout time.Duration

	// Retry là chính sách thử lại. Zero value nghĩa là dùng mặc định của core/retry.
	Retry retry.Policy

	// Breaker bật circuit breaker. nil thì không có breaker.
	Breaker *BreakerConfig

	// TLS cấu hình TLS cho kết nối ra.
	TLS tlsx.Options

	// MaxIdleConns là số connection rảnh giữ lại. <= 0 thì dùng DefaultMaxIdleConns.
	MaxIdleConns int

	// Logger ghi log request và response. nil thì không log.
	Logger *slog.Logger

	// Mask cấu hình che dữ liệu nhạy cảm khi log.
	Mask log.MaskConfig

	// PropagateTrace tự chèn header traceparent lấy từ context.
	//
	// Bật khi gọi service nội bộ: đó là điều kiện để một trace đi xuyên nhiều
	// service. Tắt khi gọi ra bên ngoài — trace ID nội bộ không nên lộ ra ngoài.
	PropagateTrace bool

	// Metrics là registry để ghi metric outbound. nil thì không ghi.
	Metrics *prometheus.Registry

	// Transport thay http.Transport mặc định, dùng cho test.
	Transport http.RoundTripper
}

// Request là một lời gọi HTTP đi ra.
type Request struct {
	// Method mặc định là GET nếu rỗng.
	Method string
	// Path là đường dẫn tương đối so với BaseURL, hoặc URL đầy đủ.
	Path string
	// Query là tham số query.
	Query url.Values
	// Header là header bổ sung.
	Header http.Header
	// Body được encode thành JSON nếu không phải []byte, io.Reader hay nil.
	Body any
}

// Response là kết quả một lời gọi.
type Response struct {
	// Status là HTTP status code.
	Status int
	// Header là header của response.
	Header http.Header
	// Body là toàn bộ body đã đọc.
	Body []byte
}

// Client là HTTP client có retry, breaker, trace và log.
//
// An toàn khi dùng từ nhiều goroutine. Dựng một lần cho mỗi đích cần gọi rồi dùng
// chung — mỗi Client giữ một connection pool riêng, nên dựng mới ở từng lời gọi sẽ
// mở lại connection mỗi lần.
type Client struct {
	cfg     Config
	http    *http.Client
	base    *url.URL
	breaker *breaker

	total    *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// New dựng Client.
func New(cfg Config) (*Client, error) {
	c := &Client{cfg: cfg}

	if cfg.BaseURL != "" {
		base, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("client: BaseURL %q không hợp lệ: %w", cfg.BaseURL, err)
		}
		if base.Scheme == "" || base.Host == "" {
			return nil, fmt.Errorf("client: BaseURL %q thiếu scheme hoặc host", cfg.BaseURL)
		}
		c.base = base
	}

	if c.cfg.Timeout <= 0 {
		c.cfg.Timeout = DefaultTimeout
	}
	if c.cfg.TotalTimeout <= 0 {
		c.cfg.TotalTimeout = DefaultTotalTimeout
	}

	transport := cfg.Transport
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.MaxIdleConns = cfg.MaxIdleConns
		if t.MaxIdleConns <= 0 {
			t.MaxIdleConns = DefaultMaxIdleConns
		}
		t.MaxIdleConnsPerHost = t.MaxIdleConns

		if hasTLS(cfg.TLS) {
			tlsCfg, err := tlsx.ClientConfig(cfg.TLS)
			if err != nil {
				return nil, fmt.Errorf("client: cấu hình TLS: %w", err)
			}
			t.TLSClientConfig = tlsCfg
		}
		transport = t
	}

	// Không đặt Timeout ở http.Client: nó tính cho cả việc đọc body, và ở đây thời
	// gian mỗi lần thử đã được kiểm soát bằng context.
	c.http = &http.Client{Transport: transport}

	if cfg.Breaker != nil {
		c.breaker = newBreaker(*cfg.Breaker)
	}
	if cfg.Metrics != nil {
		if err := c.registerMetrics(cfg.Metrics); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func hasTLS(o tlsx.Options) bool {
	return len(o.CertPEM) > 0 || o.CertFile != "" || o.CertB64 != "" ||
		len(o.RootCAPEM) > 0 || o.RootCAFile != "" || o.RootCAB64 != "" ||
		o.InsecureSkipVerify || o.ServerName != ""
}

func (c *Client) registerMetrics(reg *prometheus.Registry) error {
	c.total = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_client_requests_total",
		Help: "Tổng số request HTTP gửi ra.",
	}, []string{"host", "method", "status"})

	c.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_client_request_duration_seconds",
		Help:    "Thời gian một lời gọi HTTP ra ngoài, tính cả retry.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"host", "method"})

	if err := reg.Register(c.total); err != nil {
		return fmt.Errorf("client: đăng ký metric: %w", err)
	}
	if err := reg.Register(c.duration); err != nil {
		return fmt.Errorf("client: đăng ký metric: %w", err)
	}
	return nil
}

// BreakerState trả về trạng thái breaker, hoặc StateClosed nếu không bật breaker.
func (c *Client) BreakerState() BreakerState {
	if c.breaker == nil {
		return StateClosed
	}
	return c.breaker.currentState()
}

// Do gửi request, có retry và breaker.
//
// Trả về *Response cho mọi status code, kể cả 4xx và 5xx — status là dữ liệu, không
// phải lỗi của Go. Lỗi trả về chỉ dành cho việc **không gọi được**: mạng lỗi, hết
// thời gian, breaker mở.
//
// Không retry 4xx: chúng không tự khỏi. Retry 5xx, 429 và lỗi mạng.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	u, err := c.resolve(req)
	if err != nil {
		return nil, err
	}

	body, contentType, err := encodeBody(req.Body)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.TotalTimeout)
	defer cancel()

	start := time.Now()
	policy := c.cfg.Retry
	if policy.Retryable == nil {
		policy.Retryable = retryable
	}

	// Giữ lại response của lần thử cuối: retry.DoValue trả về giá trị zero khi
	// lỗi, mà chi tiết của một lỗi 5xx nằm chính trong body của response đó.
	var last *Response
	resp, err := retry.DoValue(ctx, policy, func(ctx context.Context) (*Response, error) {
		r, attemptErr := c.attempt(ctx, req, u, body, contentType)
		if r != nil {
			last = r
		}
		return r, attemptErr
	})
	if resp == nil {
		resp = last
	}

	// statusError chỉ là tín hiệu nội bộ cho vòng retry. Đã nhận được response từ
	// server thì đó không phải lỗi "không gọi được" — status là dữ liệu, và chỗ gọi
	// tự quyết định 500 có phải lỗi với nghiệp vụ của họ hay không.
	if resp != nil {
		var se *statusError
		if errors.As(err, &se) {
			err = nil
		}
	}

	if c.duration != nil {
		c.duration.WithLabelValues(u.Host, methodOf(req)).Observe(time.Since(start).Seconds())
	}
	if c.total != nil {
		status := "error"
		if resp != nil {
			status = strconv.Itoa(resp.Status)
		}
		c.total.WithLabelValues(u.Host, methodOf(req), status).Inc()
	}
	return resp, err
}

// attempt là một lần thử duy nhất.
func (c *Client) attempt(ctx context.Context, req Request, u *url.URL, body []byte, contentType string) (*Response, error) {
	if c.breaker != nil {
		if err := c.breaker.allow(); err != nil {
			return nil, err
		}
	}

	attemptCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		// Reader mới cho mỗi lần thử: một reader đã đọc hết thì lần retry sẽ gửi
		// body rỗng, và đó là loại bug rất khó thấy.
		reader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(attemptCtx, methodOf(req), u.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("client: dựng request: %w", err)
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	if contentType != "" && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	if c.cfg.PropagateTrace {
		if sc, ok := tracectx.FromContext(ctx); ok {
			// Span con cho chặng đi ra: cùng trace, span riêng.
			if tp := sc.NewChild().Traceparent(); tp != "" {
				httpReq.Header.Set(tracectx.HeaderTraceparent, tp)
			}
		}
	}

	started := time.Now()
	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		c.recordResult(false)
		c.logCall(ctx, req, u, body, nil, time.Since(started), err)
		return nil, fmt.Errorf("client: gọi %s: %w", u.Redacted(), err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	respBody, readErr := io.ReadAll(httpResp.Body)
	if readErr != nil {
		c.recordResult(false)
		c.logCall(ctx, req, u, body, nil, time.Since(started), readErr)
		return nil, fmt.Errorf("client: đọc response từ %s: %w", u.Redacted(), readErr)
	}

	resp := &Response{
		Status: httpResp.StatusCode,
		Header: httpResp.Header,
		Body:   respBody,
	}

	// 5xx tính là lỗi với breaker: đích đang có vấn đề. 4xx thì không — đó là lỗi
	// của request, và mở breaker vì client gửi sai là nhầm hẳn nguyên nhân.
	c.recordResult(httpResp.StatusCode < http.StatusInternalServerError)
	c.logCall(ctx, req, u, body, resp, time.Since(started), nil)

	if isRetryableStatus(httpResp.StatusCode) {
		// Trả cả resp và err để retry biết phải thử lại, còn lần cuối vẫn có response.
		return resp, &statusError{status: httpResp.StatusCode}
	}
	return resp, nil
}

func (c *Client) recordResult(success bool) {
	if c.breaker != nil {
		c.breaker.record(success)
	}
}

// logCall ghi log một lần gọi, với body đã che.
func (c *Client) logCall(ctx context.Context, req Request, u *url.URL, reqBody []byte, resp *Response, elapsed time.Duration, err error) {
	if c.cfg.Logger == nil {
		return
	}

	attrs := []slog.Attr{
		slog.String("method", methodOf(req)),
		// Redacted() bỏ mật khẩu trong phần userinfo của URL — chỗ rất dễ bị lộ.
		slog.String("url", u.Redacted()),
		slog.Int64("elapsed_ms", elapsed.Milliseconds()),
	}
	if len(reqBody) > 0 {
		attrs = append(attrs, slog.Any("request", maskBody(reqBody, c.cfg.Mask)))
	}
	if resp != nil {
		attrs = append(attrs, slog.Int("status", resp.Status))
		if len(resp.Body) > 0 {
			attrs = append(attrs, slog.Any("response", maskBody(resp.Body, c.cfg.Mask)))
		}
	}

	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.Any("error", err))
	} else if resp != nil && resp.Status >= http.StatusBadRequest {
		level = slog.LevelWarn
	}

	c.cfg.Logger.LogAttrs(ctx, level, "http client", attrs...)
}

// maskBody che body trước khi ghi log.
func maskBody(raw []byte, mask log.MaskConfig) any {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err == nil {
		return log.SafeMap(m, mask)
	}
	// Không phải JSON object: để lớp 1 của core/log xử lý theo kích thước.
	return log.Safe(string(raw))
}

// resolve ghép BaseURL, Path và Query thành URL đầy đủ.
func (c *Client) resolve(req Request) (*url.URL, error) {
	ref, err := url.Parse(req.Path)
	if err != nil {
		return nil, fmt.Errorf("client: Path %q không hợp lệ: %w", req.Path, err)
	}

	u := ref
	if c.base != nil {
		u = c.base.ResolveReference(ref)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("client: không dựng được URL đầy đủ từ %q (thiếu BaseURL?)", req.Path)
	}

	if len(req.Query) > 0 {
		q := u.Query()
		for k, vs := range req.Query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u, nil
}

func methodOf(req Request) string {
	if req.Method == "" {
		return http.MethodGet
	}
	return req.Method
}

// encodeBody chuyển Body thành bytes và xác định Content-Type.
func encodeBody(body any) (data []byte, contentType string, err error) {
	switch b := body.(type) {
	case nil:
		return nil, "", nil
	case []byte:
		return b, "", nil
	case string:
		return []byte(b), "", nil
	case io.Reader:
		// Đọc hết vào bộ nhớ: retry cần gửi lại body, mà một Reader chỉ đọc được
		// một lần. Body quá lớn để giữ trong RAM thì không nên dùng client có retry.
		data, err = io.ReadAll(b)
		if err != nil {
			return nil, "", fmt.Errorf("client: đọc body: %w", err)
		}
		return data, "", nil
	default:
		data, err = json.Marshal(body)
		if err != nil {
			return nil, "", fmt.Errorf("client: encode body thành JSON: %w", err)
		}
		return data, "application/json; charset=utf-8", nil
	}
}

// statusError bọc một status code đáng thử lại.
type statusError struct{ status int }

func (e *statusError) Error() string {
	return "client: status " + strconv.Itoa(e.status)
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// retryable là luật thử lại mặc định.
//
// Không thử lại khi breaker mở: mục đích của breaker là dừng gọi, và retry lúc đó
// chỉ làm rỗng ngân sách thời gian.
func retryable(err error) bool {
	if errors.Is(err, ErrBreakerOpen) {
		return false
	}
	if !retry.DefaultRetryable(err) {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return isRetryableStatus(se.status)
	}
	// Lỗi mạng, lỗi TLS, hết thời gian một lần thử: thử lại được.
	return true
}

// JSON gửi request và giải mã response JSON thành T.
//
// Status từ 400 trở lên thành *errs.Error với mã tương ứng, nên chỗ gọi dùng được
// errs.Is để phân loại mà không cần so sánh status code.
func JSON[T any](ctx context.Context, c *Client, req Request) (T, error) {
	var out T

	resp, err := c.Do(ctx, req)
	if err != nil {
		// Có response kèm lỗi (ví dụ hết lượt retry vì 503): phân loại theo status
		// hữu ích hơn cho chỗ gọi so với lỗi vận chuyển.
		if resp != nil && resp.Status >= http.StatusBadRequest {
			return out, statusToError(resp)
		}
		return out, err
	}
	if resp.Status >= http.StatusBadRequest {
		return out, statusToError(resp)
	}
	if len(resp.Body) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return out, fmt.Errorf("client: giải mã response JSON: %w", err)
	}
	return out, nil
}

// Get gửi GET và giải mã response JSON thành T.
func Get[T any](ctx context.Context, c *Client, path string, query url.Values) (T, error) {
	return JSON[T](ctx, c, Request{Method: http.MethodGet, Path: path, Query: query})
}

// Post gửi POST với body JSON và giải mã response thành T.
func Post[T any](ctx context.Context, c *Client, path string, body any) (T, error) {
	return JSON[T](ctx, c, Request{Method: http.MethodPost, Path: path, Body: body})
}

// statusToError biến status lỗi thành *errs.Error.
//
// Giữ nguyên phân loại của hạ nguồn: 404 của dịch vụ được gọi thành CodeNotFound,
// nên tầng gọi phân biệt được "không tìm thấy" với "dịch vụ đang lỗi" mà không phải
// đọc status code.
func statusToError(resp *Response) error {
	code := errs.CodeInternal
	switch {
	case resp.Status == http.StatusNotFound:
		code = errs.CodeNotFound
	case resp.Status == http.StatusUnauthorized:
		code = errs.CodeUnauthorized
	case resp.Status == http.StatusForbidden:
		code = errs.CodeForbidden
	case resp.Status == http.StatusConflict:
		code = errs.CodeConflict
	case resp.Status == http.StatusTooManyRequests:
		code = errs.CodeTooManyReq
	case resp.Status == http.StatusGatewayTimeout:
		code = errs.CodeTimeout
	case resp.Status == http.StatusServiceUnavailable, resp.Status == http.StatusBadGateway:
		code = errs.CodeUnavailable
	case resp.Status >= 400 && resp.Status < 500:
		code = errs.CodeBadRequest
	}

	// Cắt phần body đưa vào message: response lỗi của dịch vụ khác có thể dài, và
	// nó đi vào chuỗi lỗi của mình.
	snippet := strings.TrimSpace(string(resp.Body))
	if len(snippet) > 512 {
		snippet = snippet[:512] + "…"
	}
	return errs.New(code, fmt.Sprintf("dịch vụ phía sau trả status %d", resp.Status),
		errs.WithHTTPStatus(resp.Status),
		errs.WithCause(errors.New(snippet)))
}
