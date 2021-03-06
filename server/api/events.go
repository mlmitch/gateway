// SPDX-License-Identifier: AGPL-3.0-or-later

// Copyright (C) 2020 Mitchell Wasson

// This file is part of Weaklayer Gateway.

// Weaklayer Gateway is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.

// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package api

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/weaklayer/gateway/server/events"
	"github.com/weaklayer/gateway/server/output"
	"github.com/weaklayer/gateway/server/token"
)

// EventsAPI handles requests to the /events path
type EventsAPI struct {
	tokenProcessor token.Processor
	eventOutput    output.Output
}

// NewEventsAPI provisions an events API with its required resources
func NewEventsAPI(tokenProcessor token.Processor, eventOutput output.Output) (EventsAPI, error) {
	return EventsAPI{
		tokenProcessor: tokenProcessor,
		eventOutput:    eventOutput,
	}, nil
}

// Handle does nothing right now
func (eventsAPI EventsAPI) Handle(responseWriter http.ResponseWriter, request *http.Request) {

	encodingHeader := request.Header.Get("Content-Encoding")
	if encodingHeader != "gzip" {
		responseWriter.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}

	// Authenticate the request
	var authToken string
	authHeader := request.Header.Get("Authorization")
	if authHeader != "" {
		if strings.HasPrefix(authHeader, "Bearer ") {
			authToken = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			log.Info().Msg("Provided Authorization header does not match Bearer schema")
			responseWriter.WriteHeader(http.StatusBadRequest)
			return
		}
	} else {
		log.Info().Msg("No Authorization header provided")
		responseWriter.WriteHeader(http.StatusBadRequest)
		return
	}

	isTokenValid, claims := eventsAPI.tokenProcessor.VerifyToken(authToken)
	if !isTokenValid {
		log.Info().Msg("Invalid token provided")
		responseWriter.WriteHeader(http.StatusUnauthorized)
		return
	}

	// These are the outputs from authentication
	sensor := claims.Sensor
	group := claims.Group

	body := request.Body
	defer func() {
		err := body.Close()
		if err != nil {
			log.Info().Err(err).Msg("Failed to close request body")
		}
	}()

	gzipReader, err := gzip.NewReader(request.Body)
	if err != nil {
		log.Info().Err(err).Msg("Failed to parse gzip content")
		responseWriter.WriteHeader(http.StatusBadRequest)
		return
	}
	defer func() {
		err := gzipReader.Close()
		if err != nil {
			log.Info().Err(err).Msg("Failed to close gzip content reader")
		}
	}()

	// Start parsing the request body
	// The request body is expected to be a (potentially large) JSON array of events
	// Different event types can be mixed in the array
	decoder := json.NewDecoder(gzipReader)

	openingToken, err := decoder.Token()
	if err != nil {
		log.Info().Err(err).Msg("Could not parse request body as gzip compressed json")
		responseWriter.WriteHeader(http.StatusBadRequest)
		return
	}

	delimiter, ok := openingToken.(json.Delim)
	if !ok || delimiter != '[' {
		log.Info().Msg("Request body is not a JSON array")
		responseWriter.WriteHeader(http.StatusBadRequest)
		return
	}

	var parsedEvents []events.SensorEvent

	for decoder.More() {

		// get the bytes for the next event
		var eventData json.RawMessage
		err := decoder.Decode(&eventData)
		if err != nil {
			log.Info().Err(err).Msg("Could not parse request body JSON entry")
			responseWriter.WriteHeader(http.StatusBadRequest)
			return
		}

		// parse the bytes as a sensor event
		event, err := events.ParseEvent(eventData, sensor, group)
		if err != nil {
			log.Info().Err(err).Msg("Could not parse JSON entry as sensor event")
			responseWriter.WriteHeader(http.StatusBadRequest)
			return
		}

		parsedEvents = append(parsedEvents, event)
	}

	err = eventsAPI.eventOutput.Consume(parsedEvents)
	if err != nil {
		log.Info().Err(err).Msg("Event processing failed")
		responseWriter.WriteHeader(http.StatusInternalServerError)
		return
	}

	return
}
