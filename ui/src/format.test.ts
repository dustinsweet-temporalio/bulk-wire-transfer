// Tests for the portal's display helpers: money in integer cents, account masking, status pills, and the countdown.

import { describe, expect, it } from "vitest";
import { isTerminal, mask, money, moneyShort, phasePill, remaining, tileClass } from "./format";

describe("money", () => {
  it("formats integer cents with separators", () => {
    expect(money(1312151899)).toBe("$13,121,518.99");
    expect(money(10)).toBe("$0.10");
    expect(money(100)).toBe("$1.00");
    expect(money(-12500)).toBe("-$125.00");
  });
  it("abbreviates for cards", () => {
    expect(moneyShort(1650000000)).toBe("$16.5M");
    expect(moneyShort(250000)).toBe("$2.5K");
  });
});

describe("mask", () => {
  it("shows only the last four digits", () => {
    expect(mask("4401220033")).toBe("••0033");
    expect(mask("••0033")).toBe("••0033");
  });
});

describe("status pills", () => {
  it("maps every phase", () => {
    expect(phasePill("DRAFTED")).toEqual({ label: "Awaiting approval", tone: "warning" });
    expect(phasePill("AUTHORIZED").tone).toBe("success");
    expect(phasePill("DENIED").tone).toBe("danger");
    expect(phasePill("").label).toBe("Staged");
  });
  it("knows terminal phases", () => {
    expect(isTerminal("EXPIRED")).toBe(true);
    expect(isTerminal("AUTHORIZING")).toBe(false);
    expect(isTerminal("TERMINATED")).toBe(true);
    expect(phasePill("TERMINATED")).toEqual({ label: "Terminated", tone: "muted" });
  });
  it("maps chunk states to tiles, retrying wins", () => {
    expect(tileClass("RUNNING", false)).toBe("tile-running");
    expect(tileClass("RUNNING", true)).toBe("tile-retrying");
    expect(tileClass("SKIPPED", false)).toBe("tile-rejected");
  });
});

describe("countdown", () => {
  it("renders hours and minutes", () => {
    const now = Date.parse("2026-11-02T10:00:00Z");
    expect(remaining("2026-11-03T09:12:30Z", now)).toBe("23 h 12 m");
    expect(remaining("2026-11-02T10:01:05Z", now)).toBe("1 m 5 s");
    expect(remaining("2026-11-02T09:00:00Z", now)).toBe("expired");
  });
});
