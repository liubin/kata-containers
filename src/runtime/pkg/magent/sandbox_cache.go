package magent

import (
	"context"
	"sync"

	"github.com/containerd/containerd"
	"github.com/sirupsen/logrus"

	"encoding/json"

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/events"
	"github.com/containerd/typeurl"

	// Register grpc event types
	_ "github.com/containerd/containerd/api/events"
)

type sandboxCache struct {
	*sync.Mutex
	sandboxes map[string]string
}

func (sc *sandboxCache) getAllSandboxes() map[string]string {
	sc.Lock()
	defer sc.Unlock()
	return sc.sandboxes
}

func (sc *sandboxCache) deleteIfExists(id string) (string, bool) {
	sc.Lock()
	defer sc.Unlock()

	if val, found := sc.sandboxes[id]; found {
		delete(sc.sandboxes, id)
		return val, true
	}

	// not in sandbox cache
	return "", false
}

func (sc *sandboxCache) putIfNotExists(id, value string) bool {
	sc.Lock()
	defer sc.Unlock()

	if _, found := sc.sandboxes[id]; !found {
		sc.sandboxes[id] = value
		return true
	}

	// already in sandbox cache
	return false
}

func (sc *sandboxCache) init(sandboxes map[string]string) {
	sc.Lock()
	defer sc.Unlock()
	sc.sandboxes = sandboxes
}

// startEventsListener will boot a thread to listen container events to manage sandbox cache
func (sc *sandboxCache) startEventsListener(addr string) error {
	client, err := containerd.New(addr)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()

	eventsClient := client.EventService()
	containerClient := client.ContainerService()
	eventsCh, errCh := eventsClient.Subscribe(ctx)
	for {
		var e *events.Envelope
		select {
		case e = <-eventsCh:
		case err = <-errCh:
			logrus.WithError(err).Warn("get error from error chan")
			return err
		}

		if e != nil {
			var eventBody []byte
			if e.Event != nil {
				v, err := typeurl.UnmarshalAny(e.Event)
				if err != nil {
					logrus.WithError(err).Warn("cannot unmarshal an event from Any")
					continue
				}
				eventBody, err = json.Marshal(v)
				if err != nil {
					logrus.WithError(err).Warn("cannot marshal Any into JSON")
					continue
				}
			}

			if e.Topic == "/containers/create" {
				// Namespace: k8s.io
				// Topic: /containers/create
				// Event: {
				//          "id":"6a2e22e6fffaf1dec63ddabf587ed56069b1809ba67a0d7872fc470528364e66",
				//          "image":"k8s.gcr.io/pause:3.1",
				//          "runtime":{"name":"io.containerd.kata.v2"}
				//        }
				cc := eventstypes.ContainerCreate{}
				err := json.Unmarshal(eventBody, &cc)
				if err != nil {
					logrus.WithError(err).Warnf("unmarshal ContainerCreate failed: %s", string(eventBody))
				}

				// skip non-kata contaienrs
				if cc.Runtime.Name != kataRuntimeName {
					continue
				}

				c, err := getContainer(containerClient, e.Namespace, cc.ID)
				if err != nil {
					logrus.WithError(err).Warnf("failed to get container %s", cc.ID)
					continue
				}

				if isSandboxContainer(&c) {
					// we can simply put the contaienrid in sandboxes list if the conatiner is a sandbox container
					sc.putIfNotExists(cc.ID, e.Namespace)
					logrus.Infof("add sandbox %s to cache", cc.ID)
				}
			} else if e.Topic == "/containers/delete" {
				// Namespace: k8s.io
				// Topic: /containers/delete
				// Event: {
				//          "id":"73ec10d2e38070f930310687ab46bbaa532c79d5680fd7f18fff99f759d9385e"
				//        }
				cd := &eventstypes.ContainerDelete{}
				err := json.Unmarshal(eventBody, &cd)
				if err != nil {
					logrus.WithError(err).Warnf("unmarshal ContainerDelete failed: %s", string(eventBody))
				}

				// if container in sandboxes list, it must be the pause container in the sandbox,
				// so the contaienr id is the sandbox id
				// we can simply delete the contaienrid from sandboxes list
				_, deleted := sc.deleteIfExists(cd.ID)
				logrus.Infof("delete sandbox from cache for: %s, result: %t", cd.ID, deleted)
			} else {
				logrus.Debugf("other events: Namespace: %s, Topic: %s, Event: %s", e.Namespace, e.Topic, string(eventBody))
			}

		}
	}
}
