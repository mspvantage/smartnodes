package chain

import (
	"context"
	"strings"
	"testing"
)

func TestBridgeCalls(t *testing.T) {
	sent := `{"txhash":"AB","code":0}`
	s := &script{answers: []func([]string) ([]byte, error){
		ok(sent), ok(sent),
		ok(`{"request":{"id":"4","request_key":"rk","bridge_id":"9","sealed":"c2Vh","state":2}}`),
		ok(`{"requests":[{"id":"4","request_key":"rk"},{"request_key":"rk0"}]}`),
		ok(`{"node":{"operator":"o","tier":"1","status":"1","commitment":"abab"}}`),
		ok(`{"node":{"operator":"o","endpoint":"e:1","tier":"1","status":"1"}}`),
	}}
	c := newCLI(s)
	if err := c.RequestBridge(context.Background(), 0, "T", "RK"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.calls[0], " "); !strings.HasPrefix(got, "tx tickets request-bridge 0 T RK --from") {
		t.Fatalf("call %s", got)
	}
	sealed, state, err := c.BridgeAnswer(context.Background(), "rk")
	if err != nil || sealed != "c2Vh" || state != 2 {
		t.Fatalf("%q %d %v", sealed, state, err)
	}
	reqs, err := c.BridgeRequestsFor(context.Background(), 9)
	if err != nil || len(reqs) != 2 || reqs[0].ID != 4 || reqs[1].ID != 0 || reqs[1].RequestKey != "rk0" {
		t.Fatalf("%+v %v", reqs, err)
	}
	if fp, err := c.BridgeFingerprint(context.Background(), 9); err != nil || fp != "abab" {
		t.Fatalf("%q %v", fp, err)
	}
	if _, err := c.BridgeFingerprint(context.Background(), 9); err == nil {
		t.Fatal("a listed node gave a fingerprint")
	}
}
