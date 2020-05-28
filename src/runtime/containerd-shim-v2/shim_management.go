package containerdshim

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/namespaces"
	cdshim "github.com/containerd/containerd/runtime/v2/shim"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/sirupsen/logrus"

	"google.golang.org/grpc/codes"

	mutils "github.com/kata-containers/kata-containers/src/runtime/pkg/utils"
)

var (
	isSupportAgentMetricsAPI = true
)

func (s *service) serveMetrics(w http.ResponseWriter, r *http.Request) {
	s.sandbox.UpdateRuntimeMetrics()
	updateShimMetrics()

	// encode metrics gathered by shim
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return
	}

	encoder := expfmt.NewEncoder(w, expfmt.FmtText)
	for _, mf := range mfs {
		if err := encoder.Encode(mf); err != nil {
		}
	}

	if !isSupportAgentMetricsAPI {
		return
	}

	// get metrics from agent
	agentMetrics, err := s.sandbox.GetAgentMetrics()
	if err != nil {
		logrus.WithError(err).Error("failed GetAgentMetrics")
		if isGRPCErrorCode(codes.Unimplemented, err) {
			isSupportAgentMetricsAPI = false
		}
		return
	}

	// decode agent metrics
	reader := strings.NewReader(agentMetrics)
	decoder := expfmt.NewDecoder(reader, expfmt.FmtText)
	list := make([]*dto.MetricFamily, 0)

	for {
		mf := &dto.MetricFamily{}
		if err := decoder.Decode(mf); err != nil {
			if err == io.EOF {
				break
			}
		} else {
			list = append(list, mf)
		}
	}

	newList := make([]*dto.MetricFamily, len(list))

	for i := range list {
		m := list[i]
		// metrics collected by prometheus(prefixed by go_ and process_ ) will to add a prefix to
		// to avoid an naming conflicts
		if m.Name != nil && (strings.HasPrefix(*m.Name, "go_") || strings.HasPrefix(*m.Name, "process_")) {
			m.Name = mutils.String2Pointer("kata_agent_" + *m.Name)
		}
		newList[i] = m
	}

	for _, mf := range newList {
		encoder.Encode(mf)
	}
}

func (s *service) startManagementServer(ctx context.Context) {
	// metrics socket will under sandbox's bundle path
	metricsAddress, err := socketAddress(ctx, s.id)
	if err != nil {
		logrus.Errorf("failed to create socket address: %s", err.Error())
		return
	}
	// write metrics address to filesystem
	if err := cdshim.WriteAddress("metrics_address", metricsAddress); err != nil {
		logrus.Errorf("failed to write metrics address: %s", err.Error())
		return
	}

	listener, err := cdshim.NewSocket(metricsAddress)
	if err != nil {
		logrus.Errorf("failed to create listener: %s", err.Error())
		return
	}

	// bind hanlder
	http.HandleFunc("/metrics", s.serveMetrics)

	// start serve
	svr := &http.Server{Handler: http.DefaultServeMux}
	svr.Serve(listener)
}

func socketAddress(ctx context.Context, id string) (string, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(string(filepath.Separator), "containerd-shim", ns, id, "shim-metrics.sock"), nil
}
