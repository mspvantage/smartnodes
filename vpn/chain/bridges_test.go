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
		ok(`{"request":{"id":"4","request_key":"rk","bridge_id":"9","sealed":"c2Vh","state":2,"commitment":"abab"}}`),
		ok(`{"requests":[{"id":"4","request_key":"rk"},{"request_key":"rk0"}]}`),
		ok(`{"node":{"operator":"o","tier":"1","status":"1","commitment":"abab"}}`),
		ok(`{"node":{"operator":"o","endpoint":"e:1","tier":"1","status":"1"}}`),
	}}
	c := newCLI(s)
	if err := c.RequestBridge(context.Background(), 0, "T", "RK", ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.calls[0], " "); !strings.HasPrefix(got, "tx tickets request-bridge 0 T RK --from") {
		t.Fatalf("call %s", got)
	}
	a, err := c.BridgeAnswer(context.Background(), "rk")
	if err != nil || a.Sealed != "c2Vh" || a.State != 2 || a.ID != 4 || a.BridgeID != 9 || a.Commitment != "abab" {
		t.Fatalf("%+v %v", a, err)
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

func TestBridgeReportAndFlagCalls(t *testing.T) {
	sent := `{"txhash":"AB","code":0}`
	s := &script{answers: []func([]string) ([]byte, error){
		ok(sent), ok(sent),
		ok(`{"request":{"id":"4","request_key":"nk","bridgeId":"11","state":1}}`),
		ok(sent), ok(sent),
		ok(sent), ok(sent),
		ok(sent), ok(sent),
	}}
	c := newCLI(s)
	next, err := c.ReportBridge(context.Background(), 4, "SEC", "", "nk")
	if err != nil || next != 11 {
		t.Fatalf("%d %v", next, err)
	}
	if got := strings.Join(s.calls[0], " "); !strings.HasPrefix(got, "tx tickets report-bridge 4 --request-secret=SEC --new-request-key=nk --from") {
		t.Fatalf("call %s", got)
	}
	if _, err := c.ReportBridge(context.Background(), 4, "", "RS", ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.calls[3], " "); !strings.HasPrefix(got, "tx tickets report-bridge 4 --report-secret=RS --from") {
		t.Fatalf("call %s", got)
	}
	if err := c.RequestBridge(context.Background(), 1, "T", "RK", "H"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.calls[5], " "); !strings.HasPrefix(got, "tx tickets request-bridge 1 T RK --report-hash=H --from") {
		t.Fatalf("call %s", got)
	}
	if err := c.FlagNode(context.Background(), 3, 1, "T"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.calls[7], " "); !strings.HasPrefix(got, "tx verify flag-node 3 1 T --from") {
		t.Fatalf("call %s", got)
	}
}
