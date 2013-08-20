/*
Package apachelog is a library for logging the responses of an http.Handler in the Apache Common Log Format.
It uses a variant of this log format (http://httpd.apache.org/docs/1.3/logs.html#common) also used by
Rack::CommonLogger in Ruby. The format has an additional field at the end for the response time in seconds.

Using apachelog is typically very simple. You'll need to create an http.Handler and set up your request
routing first. In a simple web application, for example, you might just use http.NewServeMux(). Next, wrap the
http.Handler in an apachelog handler using NewHandler, create an http.Server with this handler, and you're
good to go.

Example:

		mux := http.NewServeMux()
		mux.HandleFunc("/", handler)
		loggingHandler := apachelog.NewHandler(mux, os.Stderr)
		server := &http.Server{
			Addr: ":8899",
			Handler: loggingHandler,
		}
		server.ListenAndServe()
*/
package apachelog

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Using a variant of apache common log format used in Ruby's Rack::CommonLogger which includes response time
// in seconds at the the end of the log line.
const apacheFormatPattern = "%s - - [%s] \"%s %s %s\" %d %d %0.4f\n"

var ErrHijackingNotSupported = errors.New("hijacking not supported")

// record is a wrapper around a ResponseWriter that carries other metadata needed to write a log line.
type record struct {
	http.ResponseWriter

	ip                    string
	startTime             time.Time
	method, uri, protocol string
	status                int
	responseBytes         int64
	finishTime            time.Time
	hijacked              bool
	out                   io.Writer
}

// Log writes the record out as a single log line to out.
func (r *record) Log() {
	timeFormatted := r.finishTime.Format("02/Jan/2006 15:04:05")
	elapsedTime := r.finishTime.Sub(r.startTime)
	fmt.Fprintf(r.out, apacheFormatPattern, r.ip, timeFormatted, r.method, r.uri, r.protocol, r.status,
		r.responseBytes, elapsedTime.Seconds())
}

// Write proxies to the underlying ResponseWriter.Write method while recording response size.
func (r *record) Write(p []byte) (int, error) {
	written, err := r.ResponseWriter.Write(p)
	r.responseBytes += int64(written)
	return written, err
}

// WriteHeader proxies to the underlying ResponseWriter.WriteHeader method while recording response status.
func (r *record) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *record) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, ErrHijackingNotSupported
	}
	c, rw, err := hj.Hijack()
	if err != nil {
		return c, rw, err
	}

	r.hijacked = true
	hr := hijackRecord{c, r}

	return hr, rw, nil
}

type hijackRecord struct {
	net.Conn
	r *record
}

func (h hijackRecord) Close() error {
	h.r.finishTime = time.Now()
	h.r.Log()

	return h.Conn.Close()
}

// handler is an http.Handler that logs each response.
type handler struct {
	http.Handler
	out io.Writer
}

// NewHandler creates a new http.Handler, given some underlying http.Handler to wrap and an output stream
// (typically os.Stderr).
func NewHandler(h http.Handler, out io.Writer) http.Handler {
	return &handler{
		Handler: h,
		out:     out,
	}
}

// ServeHTTP delegates to the underlying handler's ServeHTTP method and writes one log line for every call.
func (h *handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	record := &record{
		ResponseWriter: rw,
		ip:             getIP(r.RemoteAddr),
		startTime:      time.Now(),
		method:         r.Method,
		uri:            r.RequestURI,
		protocol:       r.Proto,
		status:         http.StatusOK,
		hijacked:       false,
		out:            h.out,
	}

	h.Handler.ServeHTTP(record, r)
	if !record.hijacked {
		record.finishTime = time.Now()
		record.Log()
	}
}

// A best-effort attempt at getting the IP from http.Request.RemoteAddr. For a Go server, they typically look
// like this:
// 127.0.0.1:36341
// [::1]:44092
// I think this is standard for IPv4 and IPv6 addresses.
func getIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
