import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { type APIKeyUsagePoint } from "../core/types";
import { UsageTrend } from "./api-key-usage";

const zeroPoint: APIKeyUsagePoint = {
  date: "2026-08-21",
  request_count: 0,
  error_count: 0,
  average_latency_ms: 0,
  input_tokens: 0,
  cached_input_tokens: 0,
  cache_write_input_tokens: 0,
  input_audio_tokens: 0,
  output_tokens: 0,
  reasoning_output_tokens: 0,
  output_audio_tokens: 0,
  accepted_prediction_tokens: 0,
  rejected_prediction_tokens: 0,
  total_tokens: 0,
  estimated_cost_usd: 0,
};

describe("UsageTrend", () => {
  it.each(["request_count", "total_tokens", "estimated_cost_usd"] as const)(
    "shows an empty state when %s is zero across populated date buckets",
    (metric) => {
      render(<UsageTrend points={[zeroPoint, { ...zeroPoint, date: "2026-08-22" }]} metric={metric} />);

      expect(screen.getByText("所选时间范围内该指标为 0")).toBeInTheDocument();
      expect(screen.queryByRole("img", { name: "用量趋势" })).not.toBeInTheDocument();
    },
  );

  it("renders the chart when the selected metric has a positive value", () => {
    render(<UsageTrend points={[{ ...zeroPoint, request_count: 1 }]} metric="request_count" />);

    expect(screen.getByRole("img", { name: "用量趋势" })).toBeInTheDocument();
    expect(screen.queryByText("所选时间范围内该指标为 0")).not.toBeInTheDocument();
  });
});
