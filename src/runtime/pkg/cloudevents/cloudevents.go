// Copyright (c) 2020 Ant Group
//
// SPDX-License-Identifier: Apache-2.0
//

package cloudevents

import (
	"encoding/json"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	eventstypes "github.com/containerd/containerd/api/events"
	cdruntime "github.com/containerd/containerd/runtime"
)

const (
	EventTypeNormal  = "normal"
	EventTypeWarning = "warning"
	EventTypeError   = "error"

	eventSource = "kata-containers"
)

type Publisher interface {
	Publish(event cloudevents.Event) error
	StartPublisher(path string) error
}

func NewCloudEvent(eventType, subject string, data map[string]interface{}) cloudevents.Event {
	event := cloudevents.NewEvent()
	event.SetSource(eventSource)
	event.SetID("normal")
	event.SetSubject(subject)
	event.SetType(eventType)
	_ = event.SetData(cloudevents.ApplicationJSON, data)

	return event
}

func ConvertToCloudEvent(sandbox string, e interface{}) (cloudevents.Event, error) {
	if v, ok := e.(cloudevents.Event); ok {
		return v, nil
	}

	event := cloudevents.NewEvent()
	event.SetSource(eventSource)
	eventType := EventTypeNormal

	// FIXME
	event.SetID("normal")

	var myMap map[string]interface{}
	if val, err := json.Marshal(e); err != nil {
		return cloudevents.Event{}, err
	} else {
		if err := json.Unmarshal(val, &myMap); err != nil {
			return cloudevents.Event{}, err
		}
	}

	// set sandbox id
	myMap["sandbox"] = sandbox

	_ = event.SetData(cloudevents.ApplicationJSON, myMap)

	topic := ""
	switch e.(type) {
	case *eventstypes.TaskCreate:
		topic = cdruntime.TaskCreateEventTopic
	case *eventstypes.TaskStart:
		topic = cdruntime.TaskStartEventTopic
	case *eventstypes.TaskOOM:
		topic = cdruntime.TaskOOMEventTopic
		eventType = EventTypeError
	case *eventstypes.TaskExit:
		topic = cdruntime.TaskExitEventTopic
	case *eventstypes.TaskDelete:
		topic = cdruntime.TaskDeleteEventTopic
	case *eventstypes.TaskExecAdded:
		topic = cdruntime.TaskExecAddedEventTopic
	case *eventstypes.TaskExecStarted:
		topic = cdruntime.TaskExecStartedEventTopic
	case *eventstypes.TaskPaused:
		topic = cdruntime.TaskPausedEventTopic
	case *eventstypes.TaskResumed:
		topic = cdruntime.TaskResumedEventTopic
	case *eventstypes.TaskCheckpointed:
		topic = cdruntime.TaskCheckpointedEventTopic
	default:
		topic = "UNKNOWN TOPIC"
	}

	event.SetSubject(topic)
	event.SetType(eventType)

	return event, nil
}
