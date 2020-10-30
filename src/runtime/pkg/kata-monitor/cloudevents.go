// Copyright (c) 2020 Ant Group
//
// SPDX-License-Identifier: Apache-2.0
//

package katamonitor

import (
	"context"
	"fmt"
	"net/http"
	"os"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

var eventFile *os.File

// Events handle cloud events
func (km *KataMonitor) EventsHandler() (http.Handler, error) {
	ctx := context.Background()
	p, err := cloudevents.NewHTTP()
	if err != nil {
		return nil, err
	}

	h, err := cloudevents.NewHTTPReceiveHandler(ctx, p, receive)
	if err != nil {
		return nil, err
	}

	// If the file doesn't exist, create it, or append to the file
	os.MkdirAll("/tmp/kata", 0644)
	if eventFile, err = os.OpenFile("/tmp/kata/cloudevents.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err != nil {
		return nil, err
	}

	// TODO defer close
	// if err := f.Close(); err != nil {
	// }

	return h, nil
}

func receive(ctx context.Context, event cloudevents.Event) {
	data, err := event.MarshalJSON()
	if err != nil {
		fmt.Printf("failed to MarshalJSON: %s\n", err)
	} else {
		data = append(data, '\n')
		if _, err := eventFile.Write(data); err != nil {
			fmt.Printf("failed to write file: %s\n", err)
		}
		fmt.Println("--------------------------------")
		fmt.Println(event.String())
	}
}
