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
const apacheFormatPattern = "%s - - [%s] \"%s %s %s\" %d %d %.4f\n"

var ErrHijackingNotSupported = errors.New("hijacking is not supported")

// record is a wrapper around a ResponseWriter that carries other metadata needed to write a log line.
type record struct {
	http.ResponseWriter
	out io.Writer // Same as the handler's out; the record needs to be able to log itself.

	// Only used for intermediate calculations
	startTime time.Time

	// Fields needed to produce log line
	ip                    string
	endTime               time.Time
	method, uri, protocol string
	status                int
	responseBytes         int64
	elapsedTime           time.Duration
}

// start sets up any initial state for this record before it is used to serve a request.
func (r *record) start() {
	r.startTime = time.Now()
}

// finish finalizes any data and logs the request.
func (r *record) finish() {
	r.endTime = time.Now()
	r.elapsedTime = r.endTime.Sub(r.startTime)
	r.log()
}

// log writes the record out as a single log line to r.out.
func (r *record) log() {
	timeFormatted := r.endTime.Format("02/Jan/2006 15:04:05")
	fmt.Fprintf(r.out, apacheFormatPattern, r.ip, timeFormatted, r.method, r.uri, r.protocol, r.status,
		r.responseBytes, r.elapsedTime.Seconds())
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
	w, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, ErrHijackingNotSupported
	}
	r.finish()
	return w.Hijack()
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
	rec := new(record)
	rec.start()
	rec.ResponseWriter = rw
	rec.out = h.out
	rec.ip = getIP(r.RemoteAddr)
	rec.method = r.Method
	rec.uri = r.RequestURI
	rec.protocol = r.Proto
	rec.status = http.StatusOK

	h.Handler.ServeHTTP(rec, r)
	rec.finish()
}

// getIP makes a best-effort attempt at getting the IP from http.Request.RemoteAddr. For a Go server, they
// typically look like this:
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
