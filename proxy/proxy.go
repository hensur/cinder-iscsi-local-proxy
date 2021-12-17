package proxy

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"github.com/Jeffail/gabs/v2"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func NewReverseProxy(url *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(url)

	return proxy
}

type AfterFunc func(*http.Response) error
type AfterFuncJSON func(*http.Response, *gabs.Container) error

type ReverseProxyHandler struct {
	url        *url.URL
	proxy      *httputil.ReverseProxy
	afterFuncs []AfterFunc
	logger     logrus.FieldLogger
}

func NewReverseProxyHandler(url *url.URL, logger logrus.FieldLogger) *ReverseProxyHandler {
	r := &ReverseProxyHandler{}
	r.url = url
	r.proxy = NewReverseProxy(url)
	r.logger = logger

	r.proxy.ModifyResponse = func(response *http.Response) error {
		for _, ah := range r.afterFuncs {
			err := ah(response)
			if err != nil {
				return err
			}
		}
		return nil
	}
	return r
}

func (r *ReverseProxyHandler) After(afterFuncs ...AfterFunc) *ReverseProxyHandler {
	out := NewReverseProxyHandler(r.url, r.logger)

	out.afterFuncs = append(r.afterFuncs, afterFuncs...)
	return out
}

func (r *ReverseProxyHandler) AfterJSONResponse(afterFuncsJson ...AfterFuncJSON) *ReverseProxyHandler {
	afterFuncs := make([]AfterFunc, len(afterFuncsJson))
	for i, f := range afterFuncsJson {
		afterFuncs[i] = func(response *http.Response) error {
			err := Decode(response)
			if err != nil {
				return err
			}

			// parse body json
			body, err := gabs.ParseJSONBuffer(response.Body)
			if err != nil {
				r.logger.WithError(err).Errorf("failed to parse response json, skipping handler")
				return nil
			}

			// execute function
			err = f(response, body)
			if err != nil {
				return err
			}

			// write new json
			newResponseBuffer := bytes.NewBuffer(body.Bytes())
			response.Body = ioutil.NopCloser(newResponseBuffer)
			response.Header["Content-Length"] = []string{fmt.Sprint(newResponseBuffer.Len())}
			return nil
		}
	}

	return r.After(afterFuncs...)
}

func (r *ReverseProxyHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.proxy.ServeHTTP(w, req)
}

func Decode(response *http.Response) error {
	switch response.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err := gzip.NewReader(response.Body)
		if err != nil {
			return err
		}
		response.Body = reader
		response.Header.Del("Content-Encoding")
	}
	return nil
}
