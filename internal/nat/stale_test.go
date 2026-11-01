package nat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeIPT keeps the filter/FORWARD chain in memory, in iptables order.
type fakeIPT struct {
	forward [][]string
}

func (f *fakeIPT) Exists(_, _ string, rulespec ...string) (bool, error) {
	return f.index(rulespec) >= 0, nil
}

func (f *fakeIPT) Append(_, _ string, rulespec ...string) error {
	f.forward = append(f.forward, rulespec)
	return nil
}

func (f *fakeIPT) Insert(_, _ string, pos int, rulespec ...string) error {
	i := pos - 1
	f.forward = append(f.forward[:i], append([][]string{rulespec}, f.forward[i:]...)...)
	return nil
}

func (f *fakeIPT) Delete(_, _ string, rulespec ...string) error {
	if i := f.index(rulespec); i >= 0 {
		f.forward = append(f.forward[:i], f.forward[i+1:]...)
	}
	return nil
}

func (f *fakeIPT) List(_, chain string) ([]string, error) {
	out := []string{"-P " + chain + " ACCEPT"}
	for _, r := range f.forward {
		out = append(out, "-A "+chain+" "+strings.Join(r, " "))
	}
	return out, nil
}

func (f *fakeIPT) index(rulespec []string) int {
	want := strings.Join(rulespec, " ")
	for i, r := range f.forward {
		if strings.Join(r, " ") == want {
			return i
		}
	}
	return -1
}

var kubeForwardJump = []string{"-m", "comment", "--comment", "kubernetes forwarding rules", "-j", "KUBE-FORWARD"}

// withProcSys points the sysctl writes at a temp dir for the test.
func withProcSys(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "net", "netfilter"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := procSysRoot
	procSysRoot = root
	t.Cleanup(func() { procSysRoot = old })
	return root
}

func assertFirstIsReset(t *testing.T, f *fakeIPT) {
	t.Helper()
	if len(f.forward) == 0 {
		t.Fatal("FORWARD is empty")
	}
	if got, want := strings.Join(f.forward[0], " "), strings.Join(staleFlowResetRule("eth0"), " "); got != want {
		t.Errorf("first FORWARD rule = %q, want %q", got, want)
	}
}

// kube-proxy drops INVALID packets in KUBE-FORWARD, so the reset rule only
// takes effect if it comes before that jump.
func TestEnsureStaleFlowResetGoesBeforeKubeProxy(t *testing.T) {
	root := withProcSys(t)
	f := &fakeIPT{forward: [][]string{kubeForwardJump}}
	m := &iptablesManager{ipt: f}

	if err := m.EnsureStaleFlowReset("eth0"); err != nil {
		t.Fatal(err)
	}
	assertFirstIsReset(t, f)

	got, err := os.ReadFile(filepath.Join(root, "net", "netfilter", "nf_conntrack_tcp_loose"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0" {
		t.Errorf("nf_conntrack_tcp_loose = %q, want \"0\"", got)
	}
}

func TestEnsureStaleFlowResetIdempotent(t *testing.T) {
	withProcSys(t)
	f := &fakeIPT{forward: [][]string{kubeForwardJump}}
	m := &iptablesManager{ipt: f}

	for i := 0; i < 3; i++ {
		if err := m.EnsureStaleFlowReset("eth0"); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.forward) != 2 {
		t.Errorf("want 2 FORWARD rules after repeated calls, got %d: %v", len(f.forward), f.forward)
	}
	assertFirstIsReset(t, f)
}

// A kube-proxy restart may prepend its jump again; the next reconcile must
// move the reset rule back to the front instead of leaving it behind the DROP.
func TestEnsureStaleFlowResetMovesBackToFront(t *testing.T) {
	withProcSys(t)
	f := &fakeIPT{forward: [][]string{kubeForwardJump, staleFlowResetRule("eth0")}}
	m := &iptablesManager{ipt: f}

	if err := m.EnsureStaleFlowReset("eth0"); err != nil {
		t.Fatal(err)
	}
	if len(f.forward) != 2 {
		t.Errorf("want 2 FORWARD rules, got %d: %v", len(f.forward), f.forward)
	}
	assertFirstIsReset(t, f)
}
