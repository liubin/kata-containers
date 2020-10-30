// Copyright (c) 2020 Ant Group
//
// SPDX-License-Identifier: Apache-2.0
//

package containerdshim

import (
	"context"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	kataCloudEvents "github.com/kata-containers/kata-containers/src/runtime/pkg/cloudevents"
)

func (s *service) Publish(event cloudevents.Event) error {
	s.send(event)
	return nil
}

func processCloudEvents(sandbox string, e interface{}) error {
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
