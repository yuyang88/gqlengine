// Copyright 2020 凯斐德科技（杭州）有限公司 (Karfield Technology, ltd.)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gqlengine

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

func fixCors(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}

	// use proper JSON Header
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Auth-Token, x-apollo-tracing,  Authorization, Origin, X-Requested-With")
	w.Header().Set("Access-Control-Expose-Headers", "*")

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		//w.Header().Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept, If-Modified-Since, Authorization, X-Forwarded-For")
		w.Header().Set("Access-Control-Max-Age", "86400")
		//w.Header().Add("X-Content-Type-Options", "nosniff")
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
}

func HandleHTTPOptions(w http.ResponseWriter, r *http.Request) {
	fixCors(w, r)
	w.WriteHeader(http.StatusOK)
}

const (
	ContentTypeJSON           = "application/json"
	ContentTypeGraphQL        = "application/graphql"
	ContentTypeFormURLEncoded = "application/x-www-form-urlencoded"
	ContextTypeMultipart      = "multipart/form-data"
)

type RequestOptions struct {
	Query         string                 `json:"query" url:"query" schema:"query"`
	Variables     map[string]interface{} `json:"variables" url:"variables" schema:"variables"`
	OperationName string                 `json:"operationName" url:"operationName" schema:"operationName"`
}

// a workaround for getting`variables` as a JSON string
type requestOptionsCompatibility struct {
	Query         string `json:"query" url:"query" schema:"query"`
	Variables     string `json:"variables" url:"variables" schema:"variables"`
	OperationName string `json:"operationName" url:"operationName" schema:"operationName"`
}

func getFromForm(values url.Values) *RequestOptions {
	query := values.Get("query")
	if query != "" {
		// get variables map
		variables := make(map[string]interface{}, len(values))
		variablesStr := values.Get("variables")
		_ = json.Unmarshal([]byte(variablesStr), &variables)

		return &RequestOptions{
			Query:         query,
			Variables:     variables,
			OperationName: values.Get("operationName"),
		}
	}

	return nil
}

// RequestOptions Parses a http.Request into GraphQL request options struct
func (engine *Engine) newRequestOptions(r *http.Request) []*RequestOptions {
	if reqOpt := getFromForm(r.URL.Query()); reqOpt != nil {
		return []*RequestOptions{reqOpt}
	}

	if r.Method != http.MethodPost {
		return nil
	}

	if r.Body == nil {
		return nil
	}

	// TODO: improve Content-Type handling
	contentTypeStr := r.Header.Get("Content-Type")
	contentTypeTokens := strings.Split(contentTypeStr, ";")
	contentType := contentTypeTokens[0]

	switch contentType {
	case ContentTypeGraphQL:
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return nil
		}
		return []*RequestOptions{{Query: string(body)}}

	case ContentTypeFormURLEncoded:
		if err := r.ParseForm(); err != nil {
			return nil
		}

		if reqOpt := getFromForm(r.PostForm); reqOpt != nil {
			return []*RequestOptions{reqOpt}
		}

		return nil

	case ContextTypeMultipart:
		if err := r.ParseMultipartForm(engine.opts.MultipartParsingBufferSize); err != nil {
			return nil
		}

		if reqOpts := getFromMultipart(r.MultipartForm); reqOpts != nil {
			return reqOpts
		}

		return nil

	case ContentTypeJSON:
		fallthrough
	default:
		var opts RequestOptions
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return nil
		}
		err = json.Unmarshal(body, &opts)
		if err != nil {
			// Probably `variables` was sent as a string instead of an object.
			// So, we try to be polite and try to parse that as a JSON string
			var optsCompatible requestOptionsCompatibility
			_ = json.Unmarshal(body, &optsCompatible)
			_ = json.Unmarshal([]byte(optsCompatible.Variables), &opts.Variables)
		}
		return []*RequestOptions{&opts}
	}
}
