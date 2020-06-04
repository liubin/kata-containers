package magent

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	mutils "github.com/kata-containers/kata-containers/src/runtime/pkg/utils"
	"github.com/sirupsen/logrus"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"

	dto "github.com/prometheus/client_model/go"
)

const (
	kataRuntimeName              = "io.containerd.kata.v2"
	containerdRuntimeTaskPath    = "io.containerd.runtime.v2.task"
	promNamespaceManagementAgent = "kata_magent"
	contentTypeHeader            = "Content-Type"
	contentEncodingHeader        = "Content-Encoding"
)

var (
	runningShimCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: promNamespaceManagementAgent,
		Name:      "running_shim_count",
		Help:      "Running shim count(running sandboxes).",
	})

	scrapeCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespaceManagementAgent,
		Name:      "scrape_count",
		Help:      "Scape count.",
	})

	scrapeFailedCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespaceManagementAgent,
		Name:      "scrape_failed_count",
		Help:      "Scape count.",
	})

	scrapeDurationsHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: promNamespaceManagementAgent,
		Name:      "scrape_durations_histogram_million_seconds",
		Help:      "Time used to scrape from shims",
		Buckets:   prometheus.ExponentialBuckets(1, 4, 8),
	})

	gzipPool = sync.Pool{
		New: func() interface{} {
			return gzip.NewWriter(nil)
		},
	}
)

func init() {
	prometheus.MustRegister(runningShimCount)
	prometheus.MustRegister(scrapeCount)
	prometheus.MustRegister(scrapeFailedCount)
	prometheus.MustRegister(scrapeDurationsHistogram)
}

// getMetricsAddress get metrics address for a sandbox, the abscract unix socket address is saved
// in `metrics_address` with the same place of `address`.
func (ma *MAgent) getMetricsAddress(sandboxID, namespace string) (string, error) {
	path := filepath.Join(ma.containerdStatePath, containerdRuntimeTaskPath, namespace, sandboxID, "magent_address")

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ProcessMetricsRequest get metrics from shim/hypervisor/vm/agent and return metrics to client.
func (ma *MAgent) ProcessMetricsRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	scrapeCount.Inc()
	defer func() {
		scrapeDurationsHistogram.Observe(float64(time.Since(start).Nanoseconds() / int64(time.Millisecond)))
	}()

	// gather metrics collected for management agent.

	// prepare writer for writing response.
	contentType := expfmt.Negotiate(r.Header)

	// set response header
	header := w.Header()
	header.Set(contentTypeHeader, string(contentType))

	// create writer
	writer := io.Writer(w)
	if mutils.GzipAccepted(r.Header) {
		header.Set(contentEncodingHeader, "gzip")
		gz := gzipPool.Get().(*gzip.Writer)
		defer gzipPool.Put(gz)

		gz.Reset(w)
		defer gz.Close()

		writer = gz
	}

	// create encoder to encode metrics.
	encoder := expfmt.NewEncoder(writer, contentType)

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		logrus.WithError(err).Error("failed to Gather metrics from prometheus.DefaultGatherer")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	for i := range mfs {
		metricFamily := mfs[i]

		if metricFamily.Name != nil && !strings.HasPrefix(*metricFamily.Name, promNamespaceManagementAgent) {
			metricFamily.Name = mutils.String2Pointer(promNamespaceManagementAgent + "_" + *metricFamily.Name)
		}

		// encode and write to output
		if err := encoder.Encode(metricFamily); err != nil {
			logrus.WithError(err).Warnf("failed to encode metrics: %+v", metricFamily)
		}
	}

	// aggregate sandboxes metrics and write to response by encoder
	if err := ma.aggregateSandboxMetrics(encoder); err != nil {
		logrus.WithError(err).Errorf("failed aggregateSandboxMetrics")
		scrapeFailedCount.Inc()
	}
}

// aggregateSandboxMetrics will get metrics from one sandbox and do some process
func (ma *MAgent) aggregateSandboxMetrics(encoder expfmt.Encoder) error {
	// get all sandboxes from cache
	sandboxes := ma.sandboxCache.getAllSandboxes()
	// save running kata pods as a metrics.
	runningShimCount.Set(float64(len(sandboxes)))

	// sandboxMetricsList contains list of MetricFamily list from one sandbox.
	sandboxMetricsList := make([][]*dto.MetricFamily, 0)

	// get metrics from sandbox's shim
	for sandboxID, namespace := range sandboxes {
		sandboxMetrics, err := ma.getSandboxMetrics(sandboxID, namespace)
		if err != nil {
			logrus.WithError(err).Errorf("failed to get metrics for sandbox: %s", sandboxID)
			continue
		}
		sandboxMetricsList = append(sandboxMetricsList, sandboxMetrics)
	}

	// metricsMap used to aggregate metirc from multiple sandboxes
	// key is MetricFamily.Name, and value is list of MetricFamily from multiple sandboxes
	metricsMap := make(map[string]*dto.MetricFamily)
	// merge MetricFamily list for the same MetricFamily.Name from multiple sandboxes.
	for i := range sandboxMetricsList {
		sandboxMetrics := sandboxMetricsList[i]
		for j := range sandboxMetrics {
			mf := sandboxMetrics[j]
			key := *mf.Name

			// add MetricFamily.Metric to the exists MetricFamily instance
			if oldmf, found := metricsMap[key]; found {
				oldmf.Metric = append(oldmf.Metric, mf.Metric...)
			} else {
				metricsMap[key] = mf
			}
		}
	}

	// write metrics to response.
	for _, mf := range metricsMap {
		if err := encoder.Encode(mf); err != nil {
			return err
		}
	}

	return nil
}

// getSandboxMetrics will get sandbox's metrics from shim
func (ma *MAgent) getSandboxMetrics(sandboxID, namespace string) ([]*dto.MetricFamily, error) {
	socket, err := ma.getMetricsAddress(sandboxID, namespace)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		DisableKeepAlives: true,
		Dial: func(proto, addr string) (conn net.Conn, err error) {
			return net.Dial("unix", "\x00"+socket)
		},
	}

	client := http.Client{
		Timeout:   3 * time.Second,
		Transport: transport,
	}

	resp, err := client.Get("http://shim/metrics")
	if err != nil {
		return nil, err
	}

	defer func() {
		resp.Body.Close()
	}()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	reader := bytes.NewReader(body)
	decoder := expfmt.NewDecoder(reader, expfmt.FmtText)

	// decode metrics from sandbox to MetricFamily
	list := make([]*dto.MetricFamily, 0)
	for {
		mf := &dto.MetricFamily{}
		if err := decoder.Decode(mf); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		list = append(list, mf)
	}

	// newList contains processed MetricFamily
	newList := make([]*dto.MetricFamily, len(list))

	for i := range list {
		metricFamily := list[i]
		metricList := metricFamily.Metric
		for j := range metricList {
			metric := metricList[j]
			metric.Label = append(metric.Label, &dto.LabelPair{
				Name:  mutils.String2Pointer("sandbox_id"),
				Value: mutils.String2Pointer(sandboxID),
			})
		}

		// Kata shim are using prometheus go client, add an prefix for metric name to avoid confusing
		if metricFamily.Name != nil && (strings.HasPrefix(*metricFamily.Name, "go_") || strings.HasPrefix(*metricFamily.Name, "process_")) {
			metricFamily.Name = mutils.String2Pointer("kata_shim_" + *metricFamily.Name)
		}
		newList[i] = metricFamily
	}

	return newList, nil
}
