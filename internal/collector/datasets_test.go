package collector

import (
	"context"
	"testing"

	"github.com/cdf-eagles/zpool-status-exporter/internal/runner"
)

func TestDatasetCollectorFreeBSD(t *testing.T) {
	got := collect(t, &datasetJob{run: freebsdFake(t), zfs: "zfs"})
	vol := `{dataset="datapool/authentik/disk0",pool="datapool",type="volume"}`
	want(t, got, `zfs_dataset_used_bytes`+vol, 1916928)
	want(t, got, `zfs_dataset_compress_ratio`+vol, 4.34)
	want(t, got, `zfs_dataset_volume_size_bytes`+vol, 5368709120)
	fs := `{dataset="datapool",pool="datapool",type="filesystem"}`
	want(t, got, `zfs_dataset_used_bytes`+fs, 1478601367552)
	want(t, got, `zfs_dataset_available_bytes`+fs, 452296658944)
	want(t, got, `zfs_dataset_quota_bytes`+fs, 0)
	absent(t, got, `zfs_dataset_volume_size_bytes{dataset="datapool",`)
	if n := count(got, "zfs_dataset_used_bytes{"); n != 89 {
		t.Fatalf("datasets = %d, want 89", n)
	}
}

func TestDatasetCollectorErrors(t *testing.T) {
	f := runner.NewFake(map[string]runner.Response{cmdZfsGet(): {ExitCode: 1}})
	if _, err := (&datasetJob{run: f, zfs: "zfs"}).Collect(context.Background()); err == nil {
		t.Fatal("want error")
	}
	f.Set(cmdZfsGet(), runner.Response{Stdout: "short\nx\ty\n"})
	metrics, err := (&datasetJob{run: f, zfs: "zfs"}).Collect(context.Background())
	if err != nil || len(metrics) != 0 {
		t.Fatalf("malformed lines must be skipped: %d %v", len(metrics), err)
	}
}

func TestParseFlex(t *testing.T) {
	cases := map[string]flexNum{
		"1.62": {1.62, true}, "1.62x": {1.62, true}, "-": {}, "": {}, "abc": {},
	}
	for in, w := range cases {
		if got := parseFlex(in); got != w {
			t.Fatalf("parseFlex(%q) = %+v, want %+v", in, got, w)
		}
	}
	if got := parseFlex("\t7\n"); got != (flexNum{7, true}) {
		t.Fatalf("padded number = %+v", got)
	}
	var f flexNum
	for in, w := range map[string]flexNum{`5`: {5, true}, `"2.5"`: {2.5, true}, `"-"`: {}, `null`: {}, `true`: {}} {
		if err := f.UnmarshalJSON([]byte(in)); err != nil || f != w {
			t.Fatalf("UnmarshalJSON(%s) = %+v %v", in, f, err)
		}
	}
	if err := f.UnmarshalJSON([]byte(`"unterminated`)); err == nil {
		t.Fatal("broken string must fail")
	}
}
