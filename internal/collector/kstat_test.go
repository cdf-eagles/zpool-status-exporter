package collector

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

func TestARCCollectorFreeBSD(t *testing.T) {
	got := collect(t, &arcJob{platform: freebsdPlatform(freebsdFake(t))})
	want(t, got, `zfs_arc_hits{}`, 8928424097)
	want(t, got, `zfs_arc_size{}`, 34364705608)
	want(t, got, `zfs_arc_c_max{}`, 34359738368)
	if n := count(got, "zfs_arc_"); n != 148 {
		t.Fatalf("arc metric count = %d, want 148", n)
	}
}

func TestKstatCollectorFreeBSD(t *testing.T) {
	got := collect(t, &kstatJob{platform: freebsdPlatform(freebsdFake(t))})
	want(t, got, `zfs_zil_zil_commit_count{}`, 12226652)
	want(t, got, `zfs_fm_erpt_dropped{}`, 0)
	want(t, got, `zfs_pool_iostats_arc_read_bytes_total{pool="datapool"}`, 955842125824)
	want(t, got, `zfs_pool_iostats_arc_read_total{pool="datapool"}`, 155372679)
	want(t, got, `zfs_dataset_reads_total{dataset="datapool/vm/pgsql01.sleem.net",pool="datapool"}`, 169)
	want(t, got, `zfs_dataset_writes_total{dataset="datapool/vm/pgsql01.sleem.net",pool="datapool"}`, 25)
	want(t, got, `zfs_dataset_read_bytes_total{dataset="datapool/vm/pgsql01.sleem.net",pool="datapool"}`, 334011)
	want(t, got, `zfs_dataset_written_bytes_total{dataset="datapool/vm/pgsql01.sleem.net",pool="datapool"}`, 1729)
	want(t, got, `zfs_dataset_unlinks_total{dataset="datapool/vm/pgsql01.sleem.net",pool="datapool"}`, 1)
	want(t, got, `zfs_dataset_unlinked_total{dataset="datapool/vm/pgsql01.sleem.net",pool="datapool"}`, 1)
	if n := count(got, "zfs_dataset_reads_total{"); n != 82 {
		t.Fatalf("datasets = %d, want 82", n)
	}
	absent(t, got, "zfs_arc_")
	for _, prefix := range []string{"zfs_abd_", "zfs_dbuf_", "zfs_dmu_tx_", "zfs_dnode_", "zfs_fm_", "zfs_vdev_mirror_", "zfs_zfetch_"} {
		if count(got, prefix) == 0 {
			t.Fatalf("no %s metrics", prefix)
		}
	}
}

func TestParseSysctlKstatsEdgeCases(t *testing.T) {
	snap := parseSysctlKstats([]byte(strings.Join([]string{
		"kstat.zfs.misc.zil.zil_commit_count: 5",
		"kstat.zfs.misc.zil.text_value: abc",
		"kstat.zfs.my.pool.misc.iostats.arc_read_count: 7",
		"kstat.zfs.my.pool.misc.iostats.bad: x",
		"kstat.zfs.my.pool.dataset.objset-0x1.reads: 3",
		"kstat.zfs.my.pool.dataset.objset-0x1.reads2: y",
		"kstat.zfs.my.pool.dataset.objset-0x1.dataset_name: my.pool/a b",
		"kstat.zfs.my.pool.dataset.objset-0x2.reads: 9",
		"kstat.zfs.other.misc.ddt_stats_sha256.log_flush_rate: 0 0",
		"unrelated: 1",
	}, "\n")))
	if snap.Misc["zil"]["zil_commit_count"] != 5 || len(snap.Misc["zil"]) != 1 {
		t.Fatalf("misc = %v", snap.Misc)
	}
	if len(snap.Pools) != 1 || snap.Pools[0].Pool != "my.pool" || snap.Pools[0].IOStats["arc_read_count"] != 7 {
		t.Fatalf("pools = %+v", snap.Pools)
	}
	ds := snap.Pools[0].Datasets
	if len(ds) != 1 || ds[0].Name != "my.pool/a b" || ds[0].Stats["reads"] != 3 {
		t.Fatalf("datasets (objset without a name dropped) = %+v", ds)
	}
}

func TestFreeBSDKstatErrors(t *testing.T) {
	f := runner.NewFake(map[string]runner.Response{
		cmdARC:   {Stdout: "kstat.zfs.misc.zil.x: 1\n"},
		cmdKstat: {ExitCode: 1, Stderr: "sysctl: unknown oid"},
	})
	p := freebsdPlatform(f)
	if _, err := p.ARC(context.Background()); err == nil || !strings.Contains(err.Error(), "no ARC statistics") {
		t.Fatalf("ARC: %v", err)
	}
	if _, err := (&arcJob{platform: p}).Collect(context.Background()); err == nil {
		t.Fatal("arc job must fail")
	}
	if _, err := (&kstatJob{platform: p}).Collect(context.Background()); err == nil {
		t.Fatal("kstat job must fail")
	}
	f.Set(cmdARC, runner.Response{Err: errors.New("exec")})
	if _, err := p.ARC(context.Background()); err == nil {
		t.Fatal("ARC exec error must surface")
	}
}

func linuxKstatFS(t *testing.T) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{}
	for _, name := range []string{"arcstats", "zil", "tank/iostats", "tank/objset-0x36", "tank/objset-0x99", "tank/state"} {
		m[name] = &fstest.MapFile{Data: []byte(fixture(t, "linux/kstat/"+name))}
	}
	return m
}

func TestLinuxKstats(t *testing.T) {
	p := &LinuxPlatform{KstatFS: linuxKstatFS(t)}
	got := collect(t, &arcJob{platform: p})
	want(t, got, `zfs_arc_hits{}`, 123456789)
	want(t, got, `zfs_arc_c_max{}`, 34359738368)

	got = collect(t, &kstatJob{platform: p})
	want(t, got, `zfs_zil_zil_commit_count{}`, 98765)
	want(t, got, `zfs_pool_iostats_arc_read_bytes_total{pool="tank"}`, 1048576)
	want(t, got, `zfs_dataset_read_bytes_total{dataset="tank/home",pool="tank"}`, 104857600)
	want(t, got, `zfs_dataset_writes_total{dataset="tank/media library",pool="tank"}`, 1)
}

func TestLinuxKstatErrors(t *testing.T) {
	p := &LinuxPlatform{KstatFS: fstest.MapFS{}}
	if _, err := p.ARC(context.Background()); err == nil {
		t.Fatal("missing arcstats must fail")
	}
	p.KstatFS = fstest.MapFS{"arcstats": {Data: []byte("only one line\n")}}
	if _, err := p.ARC(context.Background()); err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("headerless arcstats: %v", err)
	}
	p.KstatFS = fstest.MapFS{"zil": {Data: []byte("x\nno header here\n")}}
	if _, err := p.Kstats(context.Background()); err == nil {
		t.Fatal("bad zil file must fail")
	}
	p.KstatFS = fstest.MapFS{
		"emptypool/state":       {Data: []byte("ONLINE\n")},
		"pool2/objset-0x1":      {Data: []byte("1\nname type data\nwrites 4 1\n")},
		"pool2/objset-0x2":      {Data: []byte("garbage")},
		"pool2/objset-0x3/deep": {Data: []byte("")},
	}
	snap, err := p.Kstats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Pools) != 0 {
		t.Fatalf("pools without iostats or named datasets must be dropped: %+v", snap.Pools)
	}
	if _, err := (&LinuxPlatform{Paths: Paths{Kstat: "/nonexistent-kstat"}}).Kstats(context.Background()); err == nil {
		t.Fatal("missing kstat directory must fail")
	}
}

func TestParseProcKstat(t *testing.T) {
	nums, strs, err := parseProcKstat([]byte("1 1 0x01\nname type data\na 4 10\nb 7 x y\nc notanumber 1\nd 4\ne 4 z\n"))
	if err != nil {
		t.Fatal(err)
	}
	if nums["a"] != 10 || strs["b"] != "x y" || len(nums) != 1 {
		t.Fatalf("nums %v strs %v", nums, strs)
	}
}

func TestOSDirFS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/arcstats", []byte(fixture(t, "linux/kstat/arcstats")), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &LinuxPlatform{Paths: Paths{Kstat: dir}}
	snap, err := p.ARC(context.Background())
	if err != nil || snap.Misc["arcstats"]["hits"] != 123456789 {
		t.Fatalf("ARC from directory: %v %v", snap, err)
	}
}
