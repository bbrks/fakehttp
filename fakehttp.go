package fakehttp

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

type HTTPServer struct {
	URL           *url.URL
	Timeout       time.Duration
	started       bool
	request       chan *http.Request
	response      chan ResponseFunc
	SavedRequests []SavedRequest
}

type Response struct {
	Status  int
	Headers map[string]string
	Body    string
}

type SavedRequest struct {
	Request *http.Request
	Data    []byte
}

func NewHTTPServer() *HTTPServer {
	return NewHTTPServerWithPort(4444)
}

func NewHTTPServerWithPort(port int) *HTTPServer {
	savedRequests := []SavedRequest{}
	urlString := fmt.Sprintf("http://localhost:%d", port)
	fmt.Printf("urlString: %v\n", urlString)
	url, err := url.Parse(urlString)
	if err != nil {
		panic(fmt.Sprintf("Cannot parse url: %v", urlString))
	}
	return &HTTPServer{
		URL:           url,
		Timeout:       25 * time.Second,
		SavedRequests: savedRequests,
	}
}

type ResponseFunc func(path string) Response

func (s *HTTPServer) Start() {
	if s.started {
		return
	}
	s.started = true
	s.request = make(chan *http.Request, 1024)
	s.response = make(chan ResponseFunc, 1024)
	u := s.URL
	l, err := net.Listen("tcp", u.Host)
	if err != nil {
		panic(err)
	}
	go http.Serve(l, s)

	s.Response(203, nil, "")
	for {
		// Wait for it to be up.
		fmt.Printf("wait for it to be up: %v|\n", s.URL.String())
		resp, err := http.Get(s.URL.String())
		fmt.Printf("resp: %v err: %v\n", resp, err)
		if err == nil && resp.StatusCode == 203 {
			break
		}
		time.Sleep(1e8)
	}
	s.WaitRequest() // Consume dummy request.
}

// Flush discards all pending requests and responses.
func (s *HTTPServer) Flush() {
	for {
		select {
		case <-s.request:
		case <-s.response:
		default:
			return
		}
	}
}

func body(req *http.Request) string {
	data, err := ioutil.ReadAll(req.Body)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req.ParseMultipartForm(1e6)
	data, err := ioutil.ReadAll(req.Body)
	if err != nil {
		panic(err)
	}
	req.Body = ioutil.NopCloser(bytes.NewBuffer(data))
	savedRequest := SavedRequest{Request: req, Data: data}
	s.SavedRequests = append(s.SavedRequests, savedRequest)
	s.request <- req
	var resp Response
	select {
	case respFunc := <-s.response:
		resp = respFunc(req.URL.Path)
	case <-time.After(s.Timeout):
		const msg = "ERROR: Timeout waiting for test to prepare a response\n"
		fmt.Fprintf(os.Stderr, msg)
		resp = Response{500, nil, msg}
	}
	if resp.Headers != nil {
		h := w.Header()
		for k, v := range resp.Headers {
			h.Set(k, v)
		}
	}
	if resp.Status != 0 {
		w.WriteHeader(resp.Status)
	}
	w.Write([]byte(resp.Body))
}

// WaitRequests returns the next n requests made to the http server from
// the queue. If not enough requests were previously made, it waits until
// the timeout value for them to be made.
func (s *HTTPServer) WaitRequests(n int) []*http.Request {
	reqs := make([]*http.Request, 0, n)
	for i := 0; i < n; i++ {
		select {
		case req := <-s.request:
			reqs = append(reqs, req)
		case <-time.After(s.Timeout):
			panic("Timeout waiting for request")
		}
	}
	return reqs
}

// WaitRequest returns the next request made to the http server from
// the queue. If no requests were previously made, it waits until the
// timeout value for one to be made.
func (s *HTTPServer) WaitRequest() *http.Request {
	return s.WaitRequests(1)[0]
}

// ResponseFunc prepares the test server to respond the following n
// requests using f to build each response.
func (s *HTTPServer) ResponseFunc(n int, f ResponseFunc) {
	for i := 0; i < n; i++ {
		s.response <- f
	}
}

// ResponseMap maps request paths to responses.
type ResponseMap map[string]Response

// ResponseMap prepares the test server to respond the following n
// requests using the m to obtain the responses.
func (s *HTTPServer) ResponseMap(n int, m ResponseMap) {
	f := func(path string) Response {
		for rpath, resp := range m {
			if rpath == path {
				return resp
			}
		}
		body := "Path not found in response map: " + path
		return Response{Status: 500, Body: body}
	}
	s.ResponseFunc(n, f)
}

// Responses prepares the test server to respond the following n requests
// using the provided response parameters.
func (s *HTTPServer) Responses(n int, status int, headers map[string]string, body string) {
	f := func(path string) Response {
		return Response{status, headers, body}
	}
	s.ResponseFunc(n, f)
}

// Response prepares the test server to respond the following request
// using the provided response parameters.
func (s *HTTPServer) Response(status int, headers map[string]string, body string) {
	s.Responses(1, status, headers, body)
}
