import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { type RequestHealthBucket } from "../core/types";
import { UsageHealthTimeline, usageHealthStatus } from "./overview";

function hourlyBuckets(count: number): RequestHealthBucket[] {
  const start = Date.UTC(2026, 8, 23, 7);
  return Array.from({ length: count }, (_, index) => ({
    start: new Date(start + index * 3_600_000).toISOString(),
    total: 0,
    success: 0,
    warning: 0,
    failure: 0,
  }));
}

describe("usageHealthStatus", () => {
  it("classifies an hour by its worst meaningful outcome", () => {
    const bucket = hourlyBuckets(1)[0];
    expect(usageHealthStatus(null)).toBe("none");
    expect(usageHealthStatus(bucket)).toBe("none");
    expect(usageHealthStatus({ ...bucket, total: 10, success: 10 })).toBe("success");
    expect(usageHealthStatus({ ...bucket, total: 10, success: 9, warning: 1 })).toBe("warning");
    expect(usageHealthStatus({ ...bucket, total: 100, success: 99, failure: 1 })).toBe("warning");
    expect(usageHealthStatus({ ...bucket, total: 20, success: 19, failure: 1 })).toBe("failure");
  });
});

describe("UsageHealthTimeline", () => {
  it("renders one row per day with one cell per hour, oldest first", () => {
    const buckets = hourlyBuckets(168);
    buckets[0] = { ...buckets[0], total: 3, success: 3 };
    buckets[167] = { ...buckets[167], total: 2, success: 1, failure: 1 };

    const { container } = render(<UsageHealthTimeline buckets={buckets} />);

    const rows = container.querySelectorAll(".usage-health-row");
    expect(rows).toHaveLength(7);
    rows.forEach((row) => expect(row.querySelectorAll(".usage-health-cell")).toHaveLength(24));
    const cells = container.querySelectorAll(".usage-health-cell");
    expect(cells[0]).toHaveClass("success");
    expect(cells[0].getAttribute("title")).toContain("3");
    expect(cells[167]).toHaveClass("failure");
    expect(container.querySelectorAll(".usage-health-cell.none")).toHaveLength(166);
  });

  it("keeps an empty grid when no buckets were loaded", () => {
    const { container } = render(<UsageHealthTimeline buckets={[]} />);

    expect(container.querySelectorAll(".usage-health-row")).toHaveLength(7);
    expect(container.querySelectorAll(".usage-health-cell.none")).toHaveLength(168);
    expect(container.querySelector(".usage-health-cell[title]")).toBeNull();
  });
});
