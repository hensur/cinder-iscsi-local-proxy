package main

import (
	"flag"
	"github.com/Jeffail/gabs/v2"
	"github.com/hensur/cinder-iscsi-local-proxy/proxy"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

var (
	backend string
)

func main() {
	flag.StringVar(&backend, "backend", "localhost:8776", "cinder backend")
	flag.Parse()

	l := logrus.New()
	l.SetLevel(logrus.DebugLevel)

	backendURL, err := url.Parse(backend)
	if err != nil {
		l.WithError(err).Fatal("failed to parse backend url")
		os.Exit(1)
	}

	defaultHandler := proxy.NewReverseProxyHandler(backendURL, l)
	replaceWithLocalHandler := defaultHandler.AfterJSONResponse(replaceWithLocal(l))

	r := mux.NewRouter()
	r.Handle("/v3/{vid}/attachments/{aid}", replaceWithLocalHandler).Methods("PUT")

	r.Handle("/healthz", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))

	r.PathPrefix("/").Handler(defaultHandler)

	http.Handle("/", r)

	addr := "0.0.0.0:28776"

	l.Infof("Starting Listener on %s", addr)
	err = http.ListenAndServe(addr, nil)
	if err != nil {
		panic(err)
	}
}

func replaceWithLocal(logger logrus.FieldLogger) proxy.AfterFuncJSON {
	return func(_ *http.Response, body *gabs.Container) error {
		logger.Debugf("got json response %s", body.String())
		driverVolumeType := body.Path("attachment.connection_info.driver_volume_type")
		if driverVolumeType.Exists() {
			logger.Debugf("replacing driver_volume_type %s with %s", driverVolumeType.String(), "local")
			_, err := body.SetP("local", "attachment.connection_info.driver_volume_type")
			if err != nil {
				logger.WithError(err).Errorf("failed to set driver_volume_type")
				return nil
			}
		}

		volumeID := body.Path("attachment.volume_id").Data().(string)
		logger.Debugf("filtering for volume ID %s", volumeID)

		cmd := exec.Command("tgtadm", "--mode", "target", "--op", "show")
		out, err := cmd.Output()
		if err != nil {
			logger.WithError(err).Errorf("failed to list iscsi targets")
			return nil
		}
		logger.Debugf("tgtadmin output %s", string(out))

		var backingStoreLine string
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "Backing store path:") && strings.Contains(line, volumeID) {
				backingStoreLine = line
				break
			}
		}
		if backingStoreLine == "" {
			logger.Errorf("failed to find backing volume path")
			return nil
		}

		backingVolumePath := strings.TrimSpace(strings.Split(backingStoreLine, ":")[1])
		logger.Debugf("setting device_path %s", backingVolumePath)
		_, err = body.SetP(backingVolumePath, "attachment.connection_info.device_path")
		if err != nil {
			logger.WithError(err).Errorf("failed to set device_path")
			return nil
		}

		logger.Debugf("sending json response %s", body.String())
		return nil
	}
}
