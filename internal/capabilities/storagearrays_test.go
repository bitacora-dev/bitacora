package capabilities

import "testing"

func TestParseMDStat_HealthyDegradedAndMultipleArrays(t *testing.T) {
	arrays := ParseMDStat([]byte(`Personalities : [raid1] [raid5]
md0 : active raid1 nvme0n1p2[0] nvme1n1p2[1]
      976630336 blocks super 1.2 [2/2] [UU]
md1 : active raid5 sda1[0] sdb1[1] sdc1[2](F)
      3906762752 blocks super 1.2 [3/2] [UU_]
unused devices: <none>
`))
	if len(arrays) != 2 {
		t.Fatalf("expected two arrays, got %+v", arrays)
	}
	if got := arrays[0]; got.Name != "md0" || got.Level != "raid1" || got.MemberCount != 2 || got.Degraded {
		t.Fatalf("unexpected healthy array: %+v", got)
	}
	if got := arrays[1]; got.Name != "md1" || got.Level != "raid5" || got.MemberCount != 3 || !got.Degraded {
		t.Fatalf("unexpected degraded array: %+v", got)
	}
}

func TestParseSnapraidConfig_MultipleDataAndParityDisks(t *testing.T) {
	array := ParseSnapraidConfig([]byte(`# comment
parity /mnt/parity1/snapraid.parity
parity 2 /mnt/parity2/snapraid.parity
data media /mnt/disk1/
data backups /mnt/disk2/
content /var/snapraid.content
`))
	if array.ParityDisks != 2 || len(array.Locations) != 4 {
		t.Fatalf("unexpected SnapRAID topology: %+v", array)
	}
}
