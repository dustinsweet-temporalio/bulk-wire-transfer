// The failure catalog is complete and well formed: IDs F01–F44 in order, every card has its text blocks and either a
// trigger or narrated evidence.
import { describe, expect, it } from "vitest";
import { catalog } from "./catalog";

describe("failure catalog", () => {
  it("has every ID F01–F44 exactly once, in order", () => {
    expect(catalog.map((e) => e.id)).toEqual(Array.from({ length: 44 }, (_, i) => `F${String(i + 1).padStart(2, "0")}`));
  });
  it("gives every card its text blocks and a trigger or narrated evidence", () => {
    for (const e of catalog) {
      expect(e.see && e.handled && e.say, e.id).toBeTruthy();
      expect(e.actions.length > 0 || !!e.narrated, e.id).toBe(true);
    }
  });
  it("marks exactly F11, F39, F44 as narrated", () => {
    expect(catalog.filter((e) => e.narrated).map((e) => e.id)).toEqual(["F11", "F39", "F44"]);
  });
});
