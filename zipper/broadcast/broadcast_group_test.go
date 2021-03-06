package broadcast

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/go-graphite/carbonapi/zipper/dummy"
	"github.com/go-graphite/carbonapi/zipper/errors"
	"github.com/go-graphite/carbonapi/zipper/types"

	protov3 "github.com/go-graphite/protocol/carbonapi_v3_pb"
	"github.com/lomik/zapwriter"
	"go.uber.org/zap"
)

var logger *zap.Logger
var timeouts types.Timeouts

func init() {
	defaultLoggerConfig := zapwriter.Config{
		Logger:           "",
		File:             "stdout",
		Level:            "debug",
		Encoding:         "json",
		EncodingTime:     "iso8601",
		EncodingDuration: "seconds",
	}

	zapwriter.ApplyConfig([]zapwriter.Config{defaultLoggerConfig})

	logger = zapwriter.Logger("test")
	timeouts = types.Timeouts{
		1000 * time.Second,
		1000 * time.Second,
		1000 * time.Second,
	}
}

func errorTextsEqual(e1, e2 []error) bool {
	e1s := make([]string, 0, len(e1))
	e2s := make([]string, 0, len(e2))
	for _, e := range e1 {
		e1s = append(e1s, e.Error())
	}

	for _, e := range e2 {
		e2s = append(e2s, e.Error())
	}
	sort.Strings(e1s)
	sort.Strings(e2s)
	for i := range e1s {
		if e1s[i] != e2s[i] {
			return false
		}
	}
	return true
}

func errorsAreEqual(e1, e2 *errors.Errors) bool {
	if e1 == nil && e2 != nil {
		return false
	}

	if e1 != nil && e2 == nil {
		return false
	}

	if e1 != nil && e2 != nil {
		if e1.HaveFatalErrors != e2.HaveFatalErrors || len(e1.Errors) != len(e2.Errors) || !errorTextsEqual(e1.Errors, e2.Errors) {
			return false
		}
	}
	return true
}

type testCaseNew struct {
	name        string
	servers     []types.BackendServer
	expectedErr *errors.Errors
}

func TestNewBroadcastGroup(t *testing.T) {
	tests := []testCaseNew{
		{
			name:        "no servers",
			expectedErr: errors.Fatal("no servers specified"),
		},
		{
			name: "some servers",
			servers: []types.BackendServer{
				dummy.NewDummyClient("client1", []string{"backend1", "backend2"}, 0),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := NewBroadcastGroup(logger, tt.name, tt.servers, 60, 500, 100, timeouts)
			if !errorsAreEqual(err, tt.expectedErr) {
				t.Fatalf("unexpected error %v, expected %v", err, tt.expectedErr)
			}
			_ = b
		})
	}
}

type testCaseProbe struct {
	name            string
	servers         []types.BackendServer
	clientResponses map[string]dummy.ProbeResponse
	response        []string
	expectedErr     *errors.Errors
}

func TestProbeTLDs(t *testing.T) {
	tests := []testCaseProbe{
		{
			name: "two backends different data",
			servers: []types.BackendServer{
				dummy.NewDummyClient("client1", []string{"backend1", "backend2"}, 1),
				dummy.NewDummyClient("client2", []string{"backend3", "backend4"}, 1),
			},
			clientResponses: map[string]dummy.ProbeResponse{
				"client1": dummy.ProbeResponse{
					Response: []string{"a", "b", "c"},
				},
				"client2": dummy.ProbeResponse{
					Response: []string{"a", "d", "e"},
				},
			},
			response:    []string{"a", "b", "c", "d", "e"},
			expectedErr: &errors.Errors{},
		},
	}

	for _, tt := range tests {
		b, err := NewBroadcastGroup(logger, tt.name, tt.servers, 60, 500, 100, timeouts)
		if err != nil && (err.HaveFatalErrors || len(err.Errors) > 0) {
			t.Fatalf("error while initializing group, when it shouldn't be: %v", err)
		}

		for i := range tt.servers {
			name := fmt.Sprintf("client%v", i+1)
			s := tt.servers[i].(*dummy.DummyClient)
			s.SetTLDResponse(tt.clientResponses[name])
		}

		ctx := context.Background()

		t.Run(tt.name, func(t *testing.T) {
			res, err := b.ProbeTLDs(ctx)
			if !errorsAreEqual(err, tt.expectedErr) {
				t.Fatalf("unexpected error %v, expected %v", err, tt.expectedErr)
			}

			if len(res) != len(tt.response) {
				t.Fatalf("different amount of responses %v, expected %v", res, tt.response)
			}

			sort.Strings(res)
			sort.Strings(tt.response)
			for i := range res {
				if res[i] != tt.response[i] {
					t.Errorf("got %v, expected %v", res[i], tt.response[i])
				}
			}
		})
	}
}

type testCaseFetch struct {
	name           string
	servers        []types.BackendServer
	fetchRequest   *protov3.MultiFetchRequest
	fetchResponses map[string]dummy.FetchResponse

	expectedErr      *errors.Errors
	expectedResponse *protov3.MultiFetchResponse
}

func TestFetchRequests(t *testing.T) {
	tests := []testCaseFetch{
		{
			name: "two backends different data",
			servers: []types.BackendServer{
				dummy.NewDummyClient("client1", []string{"backend1", "backend2"}, 1),
				dummy.NewDummyClient("client2", []string{"backend3", "backend4"}, 1),
			},
			fetchRequest: &protov3.MultiFetchRequest{
				Metrics: []protov3.FetchRequest{
					{
						Name:           "foo*",
						StartTime:      0,
						StopTime:       120,
						PathExpression: "foo*",
					},
				},
			},
			fetchResponses: map[string]dummy.FetchResponse{
				"client1": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client2": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
			},

			expectedResponse: &protov3.MultiFetchResponse{
				Metrics: []protov3.FetchResponse{
					{
						Name:              "foo",
						PathExpression:    "foo*",
						ConsolidationFunc: "avg",
						StartTime:         0,
						StopTime:          120,
						StepTime:          60,
						XFilesFactor:      0.5,
						Values:            []float64{0, 1, 2},
					},
					{
						Name:              "foo2",
						PathExpression:    "foo*",
						ConsolidationFunc: "avg",
						StartTime:         0,
						StopTime:          120,
						StepTime:          60,
						XFilesFactor:      0.5,
						Values:            []float64{0, 1, 2},
					},
				},
			},
		},
		{
			name: "two backends same data",
			servers: []types.BackendServer{
				dummy.NewDummyClient("client1", []string{"backend1", "backend2"}, 1),
				dummy.NewDummyClient("client2", []string{"backend3", "backend4"}, 1),
			},
			fetchRequest: &protov3.MultiFetchRequest{
				Metrics: []protov3.FetchRequest{
					{
						Name:           "foo",
						StartTime:      0,
						StopTime:       120,
						PathExpression: "foo",
					},
				},
			},
			fetchResponses: map[string]dummy.FetchResponse{
				"client1": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client2": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
			},
			expectedResponse: &protov3.MultiFetchResponse{
				Metrics: []protov3.FetchResponse{
					{
						Name:              "foo",
						PathExpression:    "foo",
						ConsolidationFunc: "avg",
						StartTime:         0,
						StopTime:          120,
						StepTime:          60,
						XFilesFactor:      0.5,
						Values:            []float64{0, 1, 2},
					},
				},
			},
		},
		{
			name: "two backends merge data",
			servers: []types.BackendServer{
				dummy.NewDummyClient("client1", []string{"backend1", "backend2"}, 1),
				dummy.NewDummyClient("client2", []string{"backend3", "backend4"}, 1),
			},
			fetchRequest: &protov3.MultiFetchRequest{
				Metrics: []protov3.FetchRequest{
					{
						Name:           "foo",
						StartTime:      0,
						StopTime:       120,
						PathExpression: "foo",
					},
				},
			},
			fetchResponses: map[string]dummy.FetchResponse{
				"client1": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, math.NaN(), 2},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client2": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, math.NaN()},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
			},
			expectedResponse: &protov3.MultiFetchResponse{
				Metrics: []protov3.FetchResponse{
					{
						Name:              "foo",
						PathExpression:    "foo",
						ConsolidationFunc: "avg",
						StartTime:         0,
						StopTime:          120,
						StepTime:          60,
						XFilesFactor:      0.5,
						Values:            []float64{0, 1, 2},
					},
				},
			},
		},
		{
			name: "two backends different length data",
			servers: []types.BackendServer{
				dummy.NewDummyClient("client1", []string{"backend1", "backend2"}, 1),
				dummy.NewDummyClient("client2", []string{"backend3", "backend4"}, 1),
			},
			fetchRequest: &protov3.MultiFetchRequest{
				Metrics: []protov3.FetchRequest{
					{
						Name:           "foo",
						StartTime:      0,
						StopTime:       180,
						PathExpression: "foo",
					},
				},
			},
			fetchResponses: map[string]dummy.FetchResponse{
				"client1": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client2": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
			},
			expectedResponse: &protov3.MultiFetchResponse{
				Metrics: []protov3.FetchResponse{
					{
						Name:              "foo",
						PathExpression:    "foo",
						ConsolidationFunc: "avg",
						StartTime:         0,
						StopTime:          180,
						StepTime:          60,
						XFilesFactor:      0.5,
						Values:            []float64{0, 1, 2, 3},
					},
				},
			},
		},
		{
			name: "many backends, different data",
			servers: []types.BackendServer{
				dummy.NewDummyClient("client1", []string{"backend1", "backend2"}, 1),
				dummy.NewDummyClient("client2", []string{"backend3", "backend4"}, 1),
				dummy.NewDummyClient("client3", []string{"backend5", "backend6"}, 1),
				dummy.NewDummyClient("client4", []string{"backend7", "backend8"}, 1),
				dummy.NewDummyClient("client5", []string{"backend9", "backend10"}, 1),
				dummy.NewDummyClient("client6", []string{"backend11", "backend12"}, 1),
				dummy.NewDummyClient("client7", []string{"backend13", "backend14"}, 1),
				dummy.NewDummyClient("client8", []string{"backend15", "backend16"}, 1),
				dummy.NewDummyClient("client9", []string{"backend17", "backend18"}, 1),
				dummy.NewDummyClient("client10", []string{"backend19", "backend20"}, 1),
				dummy.NewDummyClient("client11", []string{"backend21", "backend22"}, 1),
				dummy.NewDummyClient("client12", []string{"backend23", "backend24"}, 1),
				dummy.NewDummyClient("client13", []string{"backend25", "backend26"}, 1),
				dummy.NewDummyClient("client14", []string{"backend27", "backend28"}, 1),
				dummy.NewDummyClient("client15", []string{"backend29", "backend30"}, 1),
				dummy.NewDummyClient("client16", []string{"backend31", "backend32"}, 1),
				dummy.NewDummyClient("client17", []string{"backend33", "backend34"}, 1),
				dummy.NewDummyClient("client18", []string{"backend35", "backend36"}, 1),
				dummy.NewDummyClient("client19", []string{"backend37", "backend38"}, 1),
				dummy.NewDummyClient("client20", []string{"backend39", "backend40"}, 1),
				dummy.NewDummyClient("client21", []string{"backend41", "backend42"}, 1),
				dummy.NewDummyClient("client22", []string{"backend43", "backend44"}, 1),
			},
			fetchRequest: &protov3.MultiFetchRequest{
				Metrics: []protov3.FetchRequest{
					{
						Name:           "foo*",
						StartTime:      0,
						StopTime:       180,
						PathExpression: "foo*",
					},
				},
			},
			fetchResponses: map[string]dummy.FetchResponse{
				"client1": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client2": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, math.NaN(), 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, math.NaN()},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client3": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          60,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client4": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client5": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client6": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client7": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client8": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client9": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client10": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client11": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client12": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          60,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client13": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client14": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, math.NaN(), 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client15": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{math.NaN(), 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client16": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client17": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client18": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client19": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client20": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client21": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
				"client22": dummy.FetchResponse{
					Response: &protov3.MultiFetchResponse{
						Metrics: []protov3.FetchResponse{
							{
								Name:              "foo",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          120,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2},
							},
							{
								Name:              "foo2",
								PathExpression:    "foo*",
								ConsolidationFunc: "avg",
								StartTime:         0,
								StopTime:          180,
								StepTime:          60,
								XFilesFactor:      0.5,
								Values:            []float64{0, 1, 2, 3},
							},
						},
					},
					Stats:  &types.Stats{},
					Errors: &errors.Errors{},
				},
			},
			expectedResponse: &protov3.MultiFetchResponse{
				Metrics: []protov3.FetchResponse{
					{
						Name:              "foo",
						PathExpression:    "foo*",
						ConsolidationFunc: "avg",
						StartTime:         0,
						StopTime:          180,
						StepTime:          60,
						XFilesFactor:      0.5,
						Values:            []float64{0, 1, 2, 3},
					},
					{
						Name:              "foo2",
						PathExpression:    "foo*",
						ConsolidationFunc: "avg",
						StartTime:         0,
						StopTime:          180,
						StepTime:          60,
						XFilesFactor:      0.5,
						Values:            []float64{0, 1, 2, 3},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		b, err := NewBroadcastGroup(logger, tt.name, tt.servers, 60, 500, 100, timeouts)
		if err != nil && (err.HaveFatalErrors || len(err.Errors) > 0) {
			t.Fatalf("error while initializing group, when it shouldn't be: %v", err)
		}

		for i := range tt.servers {
			name := fmt.Sprintf("client%v", i+1)
			s := tt.servers[i].(*dummy.DummyClient)
			resp, ok := tt.fetchResponses[name]
			if ok {
				s.AddFetchResponse(tt.fetchRequest, resp.Response, resp.Stats, resp.Errors)
			}
		}

		ctx := context.Background()

		t.Run(tt.name, func(t *testing.T) {
			res, _, err := b.Fetch(ctx, tt.fetchRequest)
			if tt.expectedErr == nil || !tt.expectedErr.HaveFatalErrors {
				if err != nil && err.HaveFatalErrors {
					t.Errorf("unexpected error %v, expected %v", err, tt.expectedErr)
				}
			} else {
				if !errorsAreEqual(err, tt.expectedErr) {
					t.Errorf("unexpected error %v, expected %v", err, tt.expectedErr)
				}
			}

			if res == nil {
				t.Fatal("result is nil")
			}

			if len(res.Metrics) != len(tt.expectedResponse.Metrics) {
				t.Fatalf("different amount of responses %v, expected %v", res, tt.expectedResponse)
			}

			sort.Slice(res.Metrics, func(i, j int) bool {
				return res.Metrics[i].Name < res.Metrics[j].Name
			})
			sort.Slice(tt.expectedResponse.Metrics, func(i, j int) bool {
				return tt.expectedResponse.Metrics[i].Name < tt.expectedResponse.Metrics[j].Name
			})
			if !reflect.DeepEqual(res, tt.expectedResponse) {
				t.Errorf("got %v, expected %v", res, tt.expectedResponse)
			}
		})
	}
}
