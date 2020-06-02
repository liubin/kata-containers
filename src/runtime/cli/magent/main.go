package main

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"

	"github.com/kata-containers/kata-containers/src/runtime/pkg/utils"

	"github.com/kata-containers/kata-containers/src/runtime/pkg/magent"
	"github.com/sirupsen/logrus"
)

var metricListenAddr = flag.String("listen-address", ":8090", "The address to listen on for HTTP requests.")
var containerdAddr = flag.String("containerd-address", "/run/containerd/containerd.sock", "Containerd address to accept client requests.")
var containerdConfig = flag.String("containerd-conf", "/etc/containerd/config.toml", "Containerd config file.")
var logLevel = flag.String("log-level", "info", "Log level of logrus(trace/debug/info/warn/error/fatal/panic).")
var logPath = flag.String("log-path", "", "Path where logs save to. Default is stdio.")

var logFile *os.File

func main() {
	flag.Parse()

	// init logrus
	initLog()

	// close log file if needed
	defer func() {
		if logFile != nil {
			logFile.Close()
		}
	}()

	// create new MAgent
	ma, err := magent.NewMAgent(*containerdAddr, *containerdConfig)
	if err != nil {
		panic(err)
	}

	// setup handlers, now only metrics is supported
	http.HandleFunc("/metrics", ma.ProcessMetricsRequest)

	// listening on the server
	logrus.Fatal(http.ListenAndServe(*metricListenAddr, nil))
}

// doing log configurations
func initLog() {
	// set log level
	level := getLogLevel(*logLevel)
	logrus.SetLevel(level)

	// set log output
	if *logPath == "" {
		// write log to stdout
		logrus.SetOutput(os.Stdout)
		logrus.Info("log path: stdout")
	} else {
		// write log to filesystem
		absPath, err := filepath.Abs(*logPath)
		if err != nil {
			panic(err)
		}

		// log file name
		file := filepath.Join(absPath, "magent.log")

		// ensure the directory is exists(create it if not)
		err = utils.EnsureFileDir(file)
		if err != nil {
			panic(err)
		}

		// open log file for writing
		logFile, err = os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0755)
		if err != nil {
			panic(err)
		}

		// set opened file as logrus' output
		logrus.SetOutput(logFile)
		logrus.Infof("log file: %s", file)
	}

	logrus.Infof("log level: %s", *logLevel)
}

// getLogLevel convert use input log level to logrus.Level
func getLogLevel(l string) logrus.Level {
	switch l {
	case "panic":
		return logrus.PanicLevel
	case "fatal":
		return logrus.FatalLevel
	case "error":
		return logrus.ErrorLevel
	case "warn":
		return logrus.WarnLevel
	case "info":
		return logrus.InfoLevel
	case "debug":
		return logrus.DebugLevel
	case "trace":
		return logrus.TraceLevel
	default:
		return logrus.InfoLevel
	}
}
