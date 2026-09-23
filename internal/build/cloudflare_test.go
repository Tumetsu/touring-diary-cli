package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudflareAuthWrittenAndRemoved(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "notes", "log.json"), `[{"timestamp": "2026-06-27T10:00:00+03:00", "text": "a"}]`)
	out := filepath.Join(dir, "dist")
	mw := filepath.Join(out, "_worker.js")
	// A gate written by an earlier version is removed silently.
	writeTestFile(t, filepath.Join(out, "functions", "_middleware.js"), "old")
	opts := Options{NotesDir: filepath.Join(dir, "notes"), OutDir: out, Title: `Lapland "2026"` + "\r\nX: y"}

	opts.CloudflareAuth = true
	sum, _, _ := buildTrip(t, opts)
	b, err := os.ReadFile(mw)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, `const REALM = "Lapland 2026 X: y";`) || !strings.Contains(src, "export default {") {
		t.Errorf("worker realm or export missing:\n%s", src[:400])
	}
	if sum.CloudflareAuth != "written" || sum.Size.Site < int64(len(b)) {
		t.Errorf("summary %q, site size %d", sum.CloudflareAuth, sum.Size.Site)
	}
	var buf strings.Builder
	if err := sum.Print(&buf); err != nil || !strings.Contains(buf.String(), "cloudflare auth: _worker.js written\n") {
		t.Errorf("summary text:\n%s", buf.String())
	}

	if _, err := os.Stat(filepath.Join(out, "functions")); !os.IsNotExist(err) {
		t.Errorf("legacy functions/ still there: %v", err)
	}

	opts.CloudflareAuth = false
	sum, _, _ = buildTrip(t, opts)
	if _, err := os.Stat(mw); !os.IsNotExist(err) {
		t.Errorf("_worker.js still there: %v", err)
	}
	if sum.CloudflareAuth != "removed" {
		t.Errorf("summary %q", sum.CloudflareAuth)
	}
	sum, _, _ = buildTrip(t, opts)
	if sum.CloudflareAuth != "" {
		t.Errorf("second build without the flag: summary %q", sum.CloudflareAuth)
	}
}

func TestCloudflareAuthKeepsOtherFunctions(t *testing.T) {
	out := t.TempDir()
	other := filepath.Join(out, "functions", "api.js")
	writeTestFile(t, other, "export function onRequest() {}\n")
	writeTestFile(t, filepath.Join(out, "functions", "_middleware.js"), "old")
	if got, err := syncCloudflareAuth(out, "T", false); err != nil || got != "" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("other function removed: %v", err)
	}
}

// TestCloudflareWorkerJS checks the worker with node: that it
// parses, and how it answers requests.
func TestCloudflareWorkerJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping the JavaScript checks")
	}
	dir := t.TempDir()
	mw := filepath.Join(dir, "worker.mjs")
	writeTestFile(t, mw, workerSource("Lapland 2026"))
	if out, err := exec.Command(node, "--check", mw).CombinedOutput(); err != nil {
		t.Fatalf("node --check: %v\n%s", err, out)
	}
	harness := filepath.Join(dir, "harness.mjs")
	writeTestFile(t, harness, `
import worker from "./worker.mjs";
const basic = (s) => "Basic " + Buffer.from(s, "utf8").toString("base64");
async function call(env, auth) {
  const headers = auth === undefined ? {} : { Authorization: auth };
  const request = new Request("https://example.com/trip.json", { headers });
  const assets = { fetch: async (req) => new Response("site " + new URL(req.url).pathname) };
  const res = await worker.fetch(request, { ...env, ASSETS: assets });
  return [res.status, await res.text(), res.headers.get("WWW-Authenticate"), res.headers.get("Cache-Control")];
}
const env = { AUTH_USER: "anna", AUTH_PASSWORD: "pöllö:€ 1" };
const cases = [
  ["no secrets", await call({}, basic("anna:pöllö:€ 1"))],
  ["no password", await call({ AUTH_USER: "anna" }, basic("anna:x"))],
  ["no header", await call(env)],
  ["wrong password", await call(env, basic("anna:pöllö:€ 2"))],
  ["wrong user", await call(env, basic("anne:pöllö:€ 1"))],
  ["prefix password", await call(env, basic("anna:pöllö"))],
  ["bad base64", await call(env, "Basic !!!")],
  ["bearer", await call(env, "Bearer abc")],
  ["ok", await call(env, basic("anna:pöllö:€ 1"))],
  ["ok lower-case scheme", await call(env, basic("anna:pöllö:€ 1").replace("Basic", "basic"))],
];
console.log(JSON.stringify(Object.fromEntries(cases)));
`)
	out, err := exec.Command(node, harness).CombinedOutput()
	if err != nil {
		t.Fatalf("node harness: %v\n%s", err, out)
	}
	got := string(out)
	const unauth = `[401,"Authentication required.\n","Basic realm=\"Lapland 2026\", charset=\"UTF-8\"","no-store"]`
	for _, want := range []string{
		`"no secrets":[500,"Site misconfigured: the AUTH_USER secret is not set.\n",null,"no-store"]`,
		`"no password":[500,"Site misconfigured: the AUTH_PASSWORD secret is not set.\n",null,"no-store"]`,
		`"no header":` + unauth,
		`"wrong password":` + unauth,
		`"wrong user":` + unauth,
		`"prefix password":` + unauth,
		`"bad base64":` + unauth,
		`"bearer":` + unauth,
		`"ok":[200,"site /trip.json",null,null]`,
		`"ok lower-case scheme":[200,"site /trip.json",null,null]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
}
