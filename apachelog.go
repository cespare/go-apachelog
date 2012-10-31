/*
apachelog is a library for logging the responses of an http.Handler in the Apache Common Log Format. It uses a
variant of this log format (http://httpd.apache.org/docs/1.3/logs.html#common) also used by Rack::CommonLogger
in Ruby. The format has an additional field at the end for the response time in seconds.

Using apachelog is typically very simple. You'll need to create an http.Handler and set up your request
routing first. In a simple web application, for example, you might just use http.DefaultServeMux. Next, wrap
the http.Handler in an apachelog.Handler using NewHandler, create an http.Server with this handler, and you're
good to go.

Example:

		mux := http.DefaultServeMux
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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Using a variant of apache common log format used in Ruby's Rack::CommonLogger which includes response time
// in seconds at the the end of the log line.
const apacheFormatPattern = "%s - - [%s] \"%s %s %s\" %d %d %0.4f\n"

// A wrapper around a ResponseWriter that carries other metadata needed to write a log line.
type Record struct {
	http.ResponseWriter

	ip                    string
	time                  time.Time
	method, uri, protocol string
	status                int
	responseBytes         int64
	elapsedTime           time.Duration
}

// Write the Record out as a single log line to out.
func (r *Record) Log(out io.Writer) {
	timeFormatted := r.time.Format("02/Jan/2006 15:04:05")
	fmt.Fprintf(out, apacheFormatPattern, r.ip, timeFormatted, r.method, r.uri, r.protocol, r.status,
		r.responseBytes, r.elapsedTime.Seconds())
}

// This proxies to the underlying ResponseWriter.Write method while recording response size.
func (r *Record) Write(p []byte) (int, error) {
	written, err := r.ResponseWriter.Write(p)
	r.responseBytes += int64(written)
	return written, err
}

// This proxies to the underlying ResponseWriter.WriteHeader method while recording response status.
func (r *Record) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// This is an http.Handler that logs each response.
type Handler struct {
	handler http.Handler
	out     io.Writer
}

// Create a new Handler, given some underlying http.Handler to wrap and an output stream (typically
// os.Stderr).
func NewHandler(handler http.Handler, out io.Writer) http.Handler {
	return &Handler{
		handler: handler,
		out:     out,
	}
}

// This delegates to the underlying handler's ServeHTTP method and writes one log line for every call.
func (h *Handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	clientIP := r.RemoteAddr
	if colon := strings.LastIndex(clientIP, ":"); colon != -1 {
		clientIP = clientIP[:colon]
	}

	record := &Record{
		ResponseWriter: rw,
		ip:             clientIP,
		time:           time.Time{},
		method:         r.Method,
		uri:            r.RequestURI,
		protocol:       r.Proto,
		status:         http.StatusOK,
		elapsedTime:    time.Duration(0),
	}

	startTime := time.Now()
	h.handler.ServeHTTP(record, r)
	finishTime := time.Now()

	record.time = finishTime
	record.elapsedTime = finishTime.Sub(startTime)

	record.Log(h.out)
}
