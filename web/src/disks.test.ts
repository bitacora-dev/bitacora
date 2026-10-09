import { describe, expect, it } from "vitest";
import type { InventoryItem } from "./api";
import { diskUsage, groupDisks, isNearlyFull, STANDALONE_GROUP, sumUsage } from "./disks";

function item(name: string, attrs: Record<string, string>): InventoryItem {
  return { id: name, name, attrs };
}

const TERABYTE = 1_000_000_000_000;

describe("diskUsage", () => {
  it("keeps usable disk values and clamps an over-reported usage ratio", () => {
    expect(diskUsage({ capacity_bytes: "100", used_bytes: "120", available_bytes: "5" })).toEqual({
      capacity: 100,
      used: 120,
      available: 5,
      ratio: 1,
    });
  });

  it("does not invent a zero usage when statfs attributes are absent or invalid", () => {
    expect(diskUsage({})).toBeNull();
    expect(diskUsage({ capacity_bytes: "100", used_bytes: "unknown" })).toBeNull();
    expect(diskUsage({ capacity_bytes: "0", used_bytes: "0" })).toBeNull();
  });
});

describe("groupDisks deduplication", () => {
  const md0 = { device: "/dev/md0", fstype: "ext4", capacity_bytes: "1000", used_bytes: "400", available_bytes: "600" };

  it("reports one disk for a device mounted several times and lists every mountpoint", () => {
    const groups = groupDisks([
      item("/", md0),
      item("/tmp", md0),
      item("/var/lib/bitacora", md0),
      item("/var/tmp", md0),
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0].disks).toHaveLength(1);
    expect(groups[0].disks[0].device).toBe("/dev/md0");
    expect(groups[0].disks[0].mounts.map((mount) => mount.mountpoint)).toEqual(["/", "/tmp", "/var/lib/bitacora", "/var/tmp"]);
  });

  it("counts a device mounted several times once in the totals", () => {
    const groups = groupDisks([item("/", md0), item("/tmp", md0), item("/var/tmp", md0)]);
    expect(groups[0].total).toEqual({ capacity: 1000, used: 400, available: 600, ratio: 0.4 });
  });

  it("takes the usage of the mount nearest the filesystem root when mounts of one filesystem disagree", () => {
    const groups = groupDisks([
      item("/var/lib/bitacora", { ...md0, capacity_bytes: "1000", used_bytes: "100" }),
      item("/", { ...md0, capacity_bytes: "1000", used_bytes: "400" }),
    ]);
    expect(groups[0].disks).toHaveLength(1);
    expect(groups[0].disks[0].usage?.used).toBe(400);
  });

  it("keeps distinct devices apart even when their attributes are otherwise identical", () => {
    const groups = groupDisks([
      item("/mnt/data1", { ...md0, device: "/dev/sdf1" }),
      item("/mnt/data2", { ...md0, device: "/dev/sdg1" }),
    ]);
    expect(groups[0].disks.map((disk) => disk.device)).toEqual(["/dev/sdf1", "/dev/sdg1"]);
    expect(groups[0].total?.capacity).toBe(2000);
  });

  it("does not merge items that report no device into a single phantom disk", () => {
    const groups = groupDisks([item("/mnt/a", { fstype: "ext4" }), item("/mnt/b", { fstype: "ext4" })]);
    expect(groups[0].disks).toHaveLength(2);
    expect(groups[0].disks.every((disk) => disk.device === null)).toBe(true);
  });

  it("recovers the device identity from whichever mount carried it", () => {
    const groups = groupDisks([
      item("/", { device: "/dev/md0" }),
      item("/tmp", { device: "/dev/md0", model: "Samsung SSD 870", serial: "S5Y1", temperature_celsius: "31", smart_status: "passed" }),
    ]);
    const disk = groups[0].disks[0];
    expect(disk.model).toBe("Samsung SSD 870");
    expect(disk.serial).toBe("S5Y1");
    expect(disk.temperatureCelsius).toBe(31);
    expect(disk.smartStatus).toBe("passed");
  });
});

describe("groupDisks grouping by function", () => {
  it("orders parity before data, arrays before standalone disks", () => {
    const snapraid = { array_type: "storage.snapraid", array_level: "2 parity disks", array_member_count: "7" };
    const groups = groupDisks([
      item("/boot/efi", { device: "/dev/nvme0n1p1", fstype: "vfat" }),
      item("/mnt/data1", { ...snapraid, device: "/dev/sdf1", array_role: "data" }),
      item("/", { device: "/dev/md0", array_type: "storage.mdraid", array_level: "raid1", array_member_count: "2", array_health: "healthy" }),
      item("/mnt/parity1", { ...snapraid, device: "/dev/sdd1", array_role: "parity" }),
    ]);

    expect(groups.map((group) => group.key)).toEqual([
      "storage.snapraid:parity",
      "storage.snapraid:data",
      "storage.mdraid",
      STANDALONE_GROUP,
    ]);
  });

  it("carries the array label on the group so the summary can name it", () => {
    const groups = groupDisks([
      item("/mnt/parity1", { device: "/dev/sdd1", array_type: "storage.snapraid", array_level: "1 parity disks", array_member_count: "6", array_role: "parity" }),
    ]);
    expect(groups[0]).toMatchObject({ arrayType: "storage.snapraid", arrayLevel: "1 parity disks", arrayMemberCount: "6" });
  });

  it("returns no group at all for an empty inventory", () => {
    expect(groupDisks([])).toEqual([]);
  });

  it("degrades the whole group when a single member is degraded", () => {
    const mdraid = { array_type: "storage.mdraid", array_level: "raid1", array_member_count: "2" };
    expect(groupDisks([item("/", { ...mdraid, device: "/dev/md0", array_health: "healthy" })])[0].arrayHealth).toBe("healthy");
    expect(
      groupDisks([
        item("/", { ...mdraid, device: "/dev/md0", array_health: "healthy" }),
        item("/srv", { ...mdraid, device: "/dev/md1", array_health: "degraded" }),
      ])[0].arrayHealth,
    ).toBe("degraded");
  });

  it("reports no array health for a group whose members never claimed one", () => {
    expect(groupDisks([item("/boot/efi", { device: "/dev/nvme0n1p1", fstype: "vfat" })])[0].arrayHealth).toBeNull();
  });
});

describe("groupDisks filesystem identity behind one device string", () => {
  const root = { device: "/dev/root", fstype: "ext4", capacity_bytes: "1000", used_bytes: "400" };

  it("keeps two filesystems apart when they report the same generic device but different filesystem ids", () => {
    const groups = groupDisks([
      item("/", { ...root, fs_id: "aaaa" }),
      item("/srv", { ...root, fs_id: "bbbb", capacity_bytes: "1000", used_bytes: "900" }),
    ]);

    expect(groups[0].disks).toHaveLength(2);
    expect(new Set(groups[0].disks.map((disk) => disk.key)).size).toBe(2);
    expect(groups[0].total?.capacity).toBe(2000);
  });

  it("folds a bind mount into its filesystem when the ids match", () => {
    const groups = groupDisks([item("/", { ...root, fs_id: "aaaa" }), item("/mnt/bind", { ...root, fs_id: "aaaa" })]);
    expect(groups[0].disks).toHaveLength(1);
    expect(groups[0].disks[0].mounts).toHaveLength(2);
    expect(groups[0].total?.capacity).toBe(1000);
  });

  it("keeps them apart without filesystem ids when their capacities differ", () => {
    const groups = groupDisks([item("/", root), item("/boot", { ...root, capacity_bytes: "500", used_bytes: "250" })]);
    expect(groups[0].disks.map((disk) => disk.usage?.capacity)).toEqual([1000, 500]);
    expect(groups[0].total?.capacity).toBe(1500);
  });

  it("folds btrfs subvolumes of one pool, which each report their own id", () => {
    const pool = { device: "/dev/sda2", fstype: "btrfs", capacity_bytes: "1000", used_bytes: "300" };
    const groups = groupDisks([item("/", { ...pool, fs_id: "1111" }), item("/home", { ...pool, fs_id: "2222" })]);
    expect(groups[0].disks).toHaveLength(1);
    expect(groups[0].total?.capacity).toBe(1000);
  });
});

describe("groupDisks readings that disagree across mounts of one device", () => {
  it("shows the worst SMART verdict and the hottest temperature, whichever mount carried them", () => {
    const sda = { device: "/dev/sda1", capacity_bytes: "1000", used_bytes: "100" };
    const [passedFirst] = groupDisks([
      item("/", { ...sda, smart_status: "passed", temperature_celsius: "35" }),
      item("/var", { ...sda, smart_status: "failed", temperature_celsius: "41" }),
    ])[0].disks;
    const [failedFirst] = groupDisks([
      item("/", { ...sda, smart_status: "failed", temperature_celsius: "41" }),
      item("/var", { ...sda, smart_status: "passed", temperature_celsius: "35" }),
    ])[0].disks;

    for (const disk of [passedFirst, failedFirst]) {
      expect(disk.smartStatus).toBe("failed");
      expect(disk.temperatureCelsius).toBe(41);
      expect(disk.state).toBe("critical");
    }
  });
});

describe("disk state", () => {
  const usable = { capacity_bytes: "1000", used_bytes: "100" };

  it("is unknown when no collector reported a SMART verdict", () => {
    expect(groupDisks([item("/", { device: "/dev/sda1", ...usable })])[0].disks[0].state).toBe("unknown");
  });

  it("is ok on a passing SMART verdict", () => {
    expect(groupDisks([item("/", { device: "/dev/sda1", smart_status: "passed", ...usable })])[0].disks[0].state).toBe("ok");
  });

  it("is critical on a failed SMART verdict or a degraded array, over a nearly-full warning", () => {
    const full = { capacity_bytes: "1000", used_bytes: "990" };
    expect(groupDisks([item("/", { device: "/dev/sda1", smart_status: "failed", ...full })])[0].disks[0].state).toBe("critical");
    expect(
      groupDisks([item("/", { device: "/dev/md1", array_type: "storage.mdraid", array_health: "degraded", smart_status: "passed", ...usable })])[0].disks[0].state,
    ).toBe("critical");
  });

  it("keeps the health light ok on a nearly-full disk whose SMART verdict passed", () => {
    // Full is not failing: the usage bar carries that warning, not the light.
    const disk = groupDisks([item("/", { device: "/dev/sda1", smart_status: "passed", capacity_bytes: "1000", used_bytes: "950" })])[0].disks[0];
    expect(disk.state).toBe("ok");
    expect(disk.usage && isNearlyFull(disk.usage)).toBe(true);
  });

  it("ignores a non-numeric temperature instead of showing NaN", () => {
    expect(groupDisks([item("/", { device: "/dev/sda1", temperature_celsius: "n/a" })])[0].disks[0].temperatureCelsius).toBeNull();
  });
});

describe("sumUsage", () => {
  it("skips disks without usage data instead of counting them as empty", () => {
    const groups = groupDisks([
      item("/mnt/data1", { device: "/dev/sdf1", capacity_bytes: String(100 * TERABYTE), used_bytes: String(60 * TERABYTE) }),
      item("/mnt/data2", { device: "/dev/sdg1" }),
    ]);
    expect(sumUsage(groups[0].disks)).toEqual({
      capacity: 100 * TERABYTE,
      used: 60 * TERABYTE,
      available: 40 * TERABYTE,
      ratio: 0.6,
    });
  });

  it("is null when nothing in the group reported usage", () => {
    expect(sumUsage(groupDisks([item("/mnt/data1", { device: "/dev/sdf1" })])[0].disks)).toBeNull();
  });
});
