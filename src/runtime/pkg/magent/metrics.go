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
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/expfmt"

	dto "github.com/prometheus/client_model/go"
)

const (
	kataRuntimeName              = "io.containerd.kata.v2"
	containerdRuntimeTaskPath    = "io.containerd.runtime.v2.task"
	promNamespaceManagementAgent = "kata_magent"
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

func (ma *MAgent) aggrateSandboxMetrics(sandboxID, namespace string, w http.ResponseWriter, r *http.Request, encoder expfmt.Encoder) error {
	socket, err := ma.getMetricsAddress(sandboxID, namespace)
	if err != nil {
		return err
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
		return err
	}

	defer func() {
		resp.Body.Close()
	}()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	reader := bytes.NewReader(body)
	decoder := expfmt.NewDecoder(reader, expfmt.FmtText)

	list := make([]*dto.MetricFamily, 0)
	for {
		mf := &dto.MetricFamily{}
		if err := decoder.Decode(mf); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		list = append(list, mf)
	}

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

		if metricFamily.Name != nil && (strings.HasPrefix(*metricFamily.Name, "go_") || strings.HasPrefix(*metricFamily.Name, "process_")) {
			metricFamily.Name = mutils.String2Pointer("kata_shim_" + *metricFamily.Name)
		}
		newList[i] = metricFamily
	}

	// encoder := expfmt.NewEncoder(w, expfmt.FmtText)
	for _, mf := range newList {
		if err := encoder.Encode(mf); err != nil {
			return err
		}
	}

	return nil
}

// ProcessMetricsRequest get metrics from shim/hypervisor/vm/agent and return metrics to client.
func (ma *MAgent) ProcessMetricsRequest(w http.ResponseWriter, r *http.Request) {
	var (
		sandboxes map[string]string
		err       error
	)
	start := time.Now()

	scrapeCount.Inc()
	defer func() {
		scrapeDurationsHistogram.Observe(float64(time.Since(start).Nanoseconds() / int64(time.Millisecond)))
		if err != nil {
			scrapeFailedCount.Inc()
		}
	}()

	// gather metrics collected for management agent.
	x := promhttp.Handler()
	x.ServeHTTP(w, r)

	// prepare writer for writing response.
	contentType := expfmt.Negotiate(r.Header)
	writer := io.Writer(w)
	if mutils.GzipAccepted(r.Header) {
		gz := gzipPool.Get().(*gzip.Writer)
		defer gzipPool.Put(gz)

		gz.Reset(w)
		defer gz.Close()

		writer = gz
	}

	// create encoder to encode metrics.
	encoder := expfmt.NewEncoder(writer, contentType)

	// get all sandboxes from cache
	sandboxes = ma.sandboxCache.getAllSandboxes()
	// save running kata pods as a metrics.
	runningShimCount.Set(float64(len(sandboxes)))
	for sandboxID, namespace := range sandboxes {
		// aggrate one sandbox's metrics
		if e := ma.aggrateSandboxMetrics(sandboxID, namespace, w, r, encoder); e != nil {
			logrus.Errorf("failed to aggrate one sandbox's metrics: %s", e.Error())
			err = e
		}
	}
}
