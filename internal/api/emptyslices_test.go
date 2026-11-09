package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmptySlices(t *testing.T) {
	type inner struct {
		Tags []string        `json:"tags"`
		Raw  json.RawMessage `json:"raw,omitempty"`
	}
	type body struct {
		Items  []inner  `json:"items"`
		Nested *inner   `json:"nested"`
		IDs    []int    `json:"ids"`
		One    inner    `json:"one"`
		Ptrs   []*inner `json:"ptrs"`
	}
	b := &body{Items: []inner{{}}, Nested: &inner{}, Ptrs: []*inner{{}, nil}}
	out, err := emptySlices(nil, "200", b)
	require.NoError(t, err)
	j, err := json.Marshal(out)
	require.NoError(t, err)
	require.JSONEq(t, `{"items":[{"tags":[]}],"nested":{"tags":[]},"ids":[],"one":{"tags":[]},"ptrs":[{"tags":[]},null]}`, string(j))

	// by value
	out, err = emptySlices(nil, "200", body{})
	require.NoError(t, err)
	j, _ = json.Marshal(out)
	require.JSONEq(t, `{"items":[],"nested":null,"ids":[],"one":{"tags":[]},"ptrs":[]}`, string(j))
}
