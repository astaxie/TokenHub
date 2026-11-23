import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { type ProviderCatalogModel } from "../core/types";
import { ProviderManualModels } from "./provider-manual-models";

function Harness({ importedIDs }: { importedIDs?: ReadonlySet<string> }) {
  const [models, setModels] = useState<ProviderCatalogModel[]>([]);
  return <ProviderManualModels importedIDs={importedIDs} models={models} onChange={setModels} />;
}

it("adds trimmed manual models, rejects invalid or duplicate IDs, and removes entries", async () => {
  const user = userEvent.setup();
  render(<Harness importedIDs={new Set(["already-imported"])} />);
  const id = screen.getByLabelText("上游模型 ID");
  const name = screen.getByLabelText("模型显示名称（可选）");
  const add = screen.getByRole("button", { name: "添加手动模型" });

  await user.click(add);
  expect(screen.getByRole("alert")).toHaveTextContent("请输入上游模型 ID");

  await user.type(id, "invalid,id");
  await user.click(add);
  expect(screen.getByRole("alert")).toHaveTextContent("请输入上游模型 ID");

  await user.clear(id);
  await user.type(id, "already-imported");
  await user.click(add);
  expect(screen.getByRole("alert")).toHaveTextContent("该模型已引入。");

  await user.clear(id);
  await user.type(id, "  private-model  ");
  await user.type(name, "  Private Model  ");
  await user.click(add);
  expect(screen.getByText("Private Model")).toBeInTheDocument();
  expect(screen.getByText("private-model")).toBeInTheDocument();
  expect(id).toHaveValue("");
  expect(name).toHaveValue("");

  await user.type(id, "private-model");
  await user.click(add);
  expect(screen.getByRole("alert")).toHaveTextContent("该手动模型已添加。");

  await user.click(screen.getByRole("button", { name: "移除手动模型 private-model" }));
  expect(screen.queryByText("Private Model")).not.toBeInTheDocument();
  expect(screen.queryByText("private-model")).not.toBeInTheDocument();
});

it("uses the upstream ID as the display name when the optional name is blank", async () => {
  const user = userEvent.setup();
  render(<Harness />);
  await user.type(screen.getByLabelText("上游模型 ID"), "private-model");
  await user.click(screen.getByRole("button", { name: "添加手动模型" }));
  expect(screen.getAllByText("private-model")).toHaveLength(2);
});
