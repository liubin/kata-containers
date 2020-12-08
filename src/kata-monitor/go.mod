module github.com/kata-containers/kata-containers/src/kata-monitor

go 1.14

require (
	github.com/kata-containers/kata-containers/src/runtime v0.0.0-20201207200725-94b9b812c7f1
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.7.1
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.10.0
	github.com/sirupsen/logrus v1.7.0
	github.com/stretchr/testify v1.6.1
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb
	google.golang.org/grpc v1.34.0
	k8s.io/cri-api v0.0.0-00010101000000-000000000000
)

replace (
	k8s.io/cri-api => k8s.io/kubernetes/staging/src/k8s.io/cri-api v0.0.0-20200826142205-e19964183377
	k8s.io/kubernetes => k8s.io/kubernetes v1.19.0
)
