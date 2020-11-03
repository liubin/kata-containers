// Copyright (c) 2020 Ant Group
//
// SPDX-License-Identifier: Apache-2.0
//

package containerdshim

import (
	"context"
	"fmt"
	"os"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	kataCloudEvents "github.com/kata-containers/kata-containers/src/runtime/pkg/cloudevents"
)

func (s *service) Publish(event cloudevents.Event) error {
	s.send(event)
	return nil
}

func (s *service) StartPublisher(path string) error {
	// If the file doesn't exist, create it, or append to the file
	os.MkdirAll(path, 0644)
	f, err := os.OpenFile(fmt.Sprintf("%s/%s.log", path, s.id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	s.eventFile = f
	return nil
}

func (s *service) processCloudEvents(e interface{}) error {
	if s.eventFile == nil {
		return nil
	}

	event, err := kataCloudEvents.ConvertToCloudEvent(s.id, e)
	if err != nil {
		return err
	}
	shimLog.WithField("event", event).Info("converted to cloud event")

	data, err := event.MarshalJSON()
	if err != nil {
		return err
	}

	data = append(data, '\n')
	if _, err := s.eventFile.Write(data); err != nil {
		return err
	}
	return nil
}

func processCloudEventsSendToRemote(sandbox string, e interface{}) error {
	event, err := kataCloudEvents.ConvertToCloudEvent(sandbox, e)
	if err != nil {
		return err
	}
	shimLog.WithField("event", event).Info("converted to cloud event")

	// The default client is HTTP.
	c, err := cloudevents.NewDefaultClient()
	if err != nil {
		return err
	}

	// Set a target.
	ctx := cloudevents.ContextWithTarget(context.Background(), "http://localhost:8090/events")

	// Send that Event.
	return c.Send(ctx, event)
}
