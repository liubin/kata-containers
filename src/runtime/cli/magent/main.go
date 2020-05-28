package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/kata-containers/kata-containers/src/runtime/pkg/magent"
)

var metricListenAddr = flag.String("listen-address", ":8090", "The address to listen on for HTTP requests.")
var containerdAddr = flag.String("containerd-address", "/run/containerd/containerd.sock", "Containerd address to accept client requests.")
var containerdConfig = flag.String("containerd-conf", "/etc/containerd/config.toml", "Containerd config file.")

func main() {
	ma, err := magent.NewMAgent(*containerdAddr, *containerdConfig)
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/metrics", ma.ProcessMetricsRequest)
	log.Fatal(http.ListenAndServe(*metricListenAddr, nil))
}
