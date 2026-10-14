package collector

import (
	"context"
	"strings"
	"testing"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

func vdevKey(name, pool, vdev, typ, class, device, disk string, extra ...string) string {
	labels := make([]string, 0, 6+len(extra))
	labels = append(labels,
		`class="`+class+`"`, `device="`+device+`"`, `disk="`+disk+`"`,
		`pool="`+pool+`"`, `vdev="`+vdev+`"`, `vdev_type="`+typ+`"`)
	labels = append(labels, extra...)
	sortStrings(labels)
	return name + "{" + strings.Join(labels, ",") + "}"
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestPoolCollectorFreeBSD(t *testing.T) {
	got := collect(t, newPoolJob(freebsdFake(t)))

	want(t, got, `zfs_pool_state{pool="datapool",state="ONLINE"}`, 1)
	want(t, got, `zfs_pool_state{pool="datapool",state="DEGRADED"}`, 0)
	want(t, got, `zfs_pool_size_bytes{pool="datapool"}`, 1992864825344)
	want(t, got, `zfs_pool_allocated_bytes{pool="datapool"}`, 705586925568)
	want(t, got, `zfs_pool_free_bytes{pool="datapool"}`, 1287277899776)
	want(t, got, `zfs_pool_fragmentation_ratio{pool="datapool"}`, 0.67)
	want(t, got, `zfs_pool_capacity_ratio{pool="datapool"}`, 0.35)
	want(t, got, `zfs_pool_dedup_ratio{pool="datapool"}`, 1)
	want(t, got, `zfs_pool_data_errors{pool="datapool"}`, 0)
	want(t, got, `zfs_pool_needs_attention{pool="datapool"}`, 0)
	want(t, got, `zfs_pool_needs_attention{pool="mediapool"}`, 1)
	absent(t, got, `zfs_pool_expand_size_bytes{`)
	absent(t, got, `zfs_pool_checkpoint_bytes{`)

	want(t, got, `zfs_pool_scan_state{function="SCRUB",pool="datapool",state="FINISHED"}`, 1)
	want(t, got, `zfs_pool_scan_start_time_seconds{pool="datapool"}`, 1791619661)
	want(t, got, `zfs_pool_scan_end_time_seconds{pool="datapool"}`, 1791621851)
	want(t, got, `zfs_pool_scan_errors{pool="datapool"}`, 0)
	want(t, got, `zfs_pool_scan_paused{pool="datapool"}`, 0)

	want(t, got, vdevKey("zfs_vdev_read_errors", "datapool", "gpt/chassis02", "disk", "normal", "ada2p1", "ada2"), 0)
	want(t, got, vdevKey("zfs_vdev_size_bytes", "datapool", "gpt/chassis02", "disk", "normal", "ada2p1", "ada2"), 2000293986304)
	want(t, got, vdevKey("zfs_vdev_state", "datapool", "gpt/chassis02", "disk", "normal", "ada2p1", "ada2", `state="ONLINE"`), 1)
	want(t, got, vdevKey("zfs_vdev_slow_ios", "rpool", "ada1p3", "disk", "normal", "ada1p3", "ada1"), 0)
	want(t, got, vdevKey("zfs_vdev_allocated_bytes", "datapool", "mirror-0", "mirror", "normal", "", ""), 705586925568)
	want(t, got, vdevKey("zfs_vdev_fragmentation_ratio", "datapool", "mirror-0", "mirror", "normal", "", ""), 0.67)
	want(t, got, vdevKey("zfs_vdev_allocated_bytes", "rpool", "mirror-0", "mirror", "normal", "", ""), 33930657792)
	want(t, got, vdevKey("zfs_vdev_size_bytes", "datapool", "mirror-1", "mirror", "log", "", ""), 119185342464)
	want(t, got, vdevKey("zfs_vdev_size_bytes", "datapool", "gpt/chassis10", "disk", "log", "da10p1", "da10"), 249953255424)
	want(t, got, vdevKey("zfs_vdev_size_bytes", "datapool", "gpt/chassis07", "disk", "l2cache", "da7p1", "da7"), 249953255424)
	want(t, got, vdevKey("zfs_vdev_size_bytes", "nvme-podman03", "gpt/nvme-podman03", "disk", "normal", "nda0p2", "nda0"), 536870912000)
	want(t, got, vdevKey("zfs_vdev_size_bytes", "mediapool", "gpt/a0d06", "disk", "normal", "da26p1", "da26"), 8001559724032)
	absent(t, got, `zfs_vdev_read_errors{class="normal",device="",disk="",pool="datapool",vdev="datapool"`)

	want(t, got, `zfs_disk_info{disk="da29",model="ATA HGST HUH728080AL",serial="SERIAL0018",wwn="5000000000000012"}`, 1)
	if n := count(got, "zfs_disk_info{"); n != 25 {
		t.Fatalf("disk_info count = %d, want 25", n)
	}
	// 6 pools; 8 pool-level families each plus vdev families.
	if n := count(got, "zfs_pool_state{"); n != 6*len(poolStates) {
		t.Fatalf("pool_state count = %d", n)
	}
}

func TestPoolCollectorUpdatesTopologyCache(t *testing.T) {
	run := freebsdFake(t)
	j := newPoolJob(run)
	j.cache = &topologyCache{maxAge: 1 << 62, now: fixedNow, refresh: func(context.Context) (topology, error) {
		t.Fatal("refresh must not run when the pool collector filled the cache")
		return nil, nil
	}}
	if _, err := j.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	topo, err := j.cache.get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := topo["mediapool"]["gpt/chassis04"]; got != (vdevInfo{VdevType: "disk", Class: "l2cache", Device: "da4p1", Disk: "da4"}) {
		t.Fatalf("topology entry = %+v", got)
	}
}

func TestPoolCollectorErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(f *runner.Fake)
		errSub string
	}{
		{"status fails", func(f *runner.Fake) { f.Set(cmdStatus, runner.Response{ExitCode: 1, Stderr: "no pools"}) }, "exited with code 1"},
		{"status not JSON", func(f *runner.Fake) { f.Set(cmdStatus, runner.Response{Stdout: "pool: x"}) }, "parse zpool status JSON"},
		{"status without pools", func(f *runner.Fake) { f.Set(cmdStatus, runner.Response{Stdout: "{}"}) }, `no "pools" object`},
		{"list fails", func(f *runner.Fake) { f.Set(cmdList, runner.Response{ExitCode: 2}) }, "exited with code 2"},
		{"list not JSON", func(f *runner.Fake) { f.Set(cmdList, runner.Response{Stdout: "["}) }, "parse zpool list JSON"},
		{"list without pools", func(f *runner.Fake) { f.Set(cmdList, runner.Response{Stdout: `{"output_version":{}}`}) }, `no "pools" object`},
		{"list vdev malformed", func(f *runner.Fake) {
			f.Set(cmdList, runner.Response{Stdout: `{"pools":{"datapool":{"name":"datapool","vdevs":{"mirror-0":5}}}}`})
		}, `vdev "mirror-0"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := freebsdFake(t)
			tc.mutate(f)
			_, err := newPoolJob(f).Collect(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("err = %v, want containing %q", err, tc.errSub)
			}
		})
	}
}

func TestPoolCollectorWithoutDeviceInventory(t *testing.T) {
	f := freebsdFake(t)
	f.Set(cmdGeom, runner.Response{ExitCode: 1, Stderr: "geom: not found"})
	got := collect(t, newPoolJob(f))
	want(t, got, vdevKey("zfs_vdev_read_errors", "datapool", "gpt/chassis02", "disk", "normal", "chassis02", ""), 0)
	absent(t, got, "zfs_disk_info{")
}

func TestPoolCollectorUnknownState(t *testing.T) {
	f := freebsdFake(t)
	f.Set(cmdStatus, runner.Response{Stdout: `{"pools":{"p":{"name":"p","state":"WEIRD","vdevs":{"p":{"name":"p","vdev_type":"root","vdevs":{` +
		`"sp":{"name":"sp","vdev_type":"disk","state":"INUSE","path":"/dev/da9"}}}},` +
		`"scan_stats":{"function":"RESILVER","state":"SCANNING","start_time":10,"end_time":0,"scrub_pause":5}}}}`})
	f.Set(cmdList, runner.Response{Stdout: `{"pools":{"p":{"name":"p","properties":{"expandsize":{"value":"-"},"checkpoint":{"value":4096}}}}}`})
	got := collect(t, newPoolJob(f))
	want(t, got, `zfs_pool_state{pool="p",state="WEIRD"}`, 1)
	want(t, got, `zfs_pool_state{pool="p",state="ONLINE"}`, 0)
	want(t, got, `zfs_pool_checkpoint_bytes{pool="p"}`, 4096)
	want(t, got, `zfs_pool_scan_paused{pool="p"}`, 1)
	absent(t, got, `zfs_pool_scan_end_time_seconds{`)
	absent(t, got, `zfs_pool_expand_size_bytes{`)
	want(t, got, vdevKey("zfs_vdev_state", "p", "sp", "disk", "normal", "da9", "da9", `state="INUSE"`), 1)
}

func TestSpareTopologyKeepsFirstClass(t *testing.T) {
	st, err := parseStatus([]byte(`{"pools":{"p":{"name":"p","vdevs":{"p":{"name":"p","vdev_type":"root","vdevs":{` +
		`"spare-0":{"name":"spare-0","vdev_type":"spare","vdevs":{"da1":{"name":"da1","vdev_type":"disk"},"da9":{"name":"da9","vdev_type":"disk"}}}}}},` +
		`"spares":{"da9":{"name":"da9","vdev_type":"disk","state":"INUSE"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	topo := buildTopology(st, DeviceMap{})
	if c := topo["p"]["da9"].Class; c != "normal" {
		t.Fatalf("in-use spare class = %q, want normal (as on its vdev series)", c)
	}
}

func TestScanEndOnlyWhenFinished(t *testing.T) {
	for state, wantEnd := range map[string]bool{"FINISHED": true, "CANCELED": false, "SCANNING": false} {
		f := freebsdFake(t)
		f.Set(cmdStatus, runner.Response{Stdout: `{"pools":{"p":{"name":"p","state":"ONLINE",` +
			`"scan_stats":{"function":"SCRUB","state":"` + state + `","start_time":10,"end_time":20}}}}`})
		f.Set(cmdList, runner.Response{Stdout: `{"pools":{"p":{"name":"p"}}}`})
		got := collect(t, newPoolJob(f))
		_, has := got[`zfs_pool_scan_end_time_seconds{pool="p"}`]
		if has != wantEnd {
			t.Fatalf("state %s: end time present = %v", state, has)
		}
	}
}

func TestFlattenPoolClasses(t *testing.T) {
	st, err := parseStatus([]byte(`{"pools":{"p":{"name":"p","vdevs":{"p":{"name":"p","vdev_type":"root","vdevs":{"d1":{"name":"d1","vdev_type":"disk"}}}},` +
		`"special":{"m":{"name":"m","vdev_type":"mirror"}},"dedup":{"dd":{"name":"dd","vdev_type":"disk"}},` +
		`"spares":{"s1":{"name":"s1","vdev_type":"disk"}},"logs":{"l":null}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range flattenPool(st.Pools["p"]) {
		got[v.Name] = v.class
	}
	wantClasses := map[string]string{"d1": "normal", "m": "special", "dd": "dedup", "s1": "spare"}
	if len(got) != len(wantClasses) {
		t.Fatalf("classes = %v", got)
	}
	for k, v := range wantClasses {
		if got[k] != v {
			t.Fatalf("class of %s = %q, want %q", k, got[k], v)
		}
	}
}
