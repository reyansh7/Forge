"use client";

import { useEffect, useState } from "react";
import { getHealth } from "./api";

export function HealthDot() {
  const [label, setLabel] = useState("checking API");
  const [kind, setKind] = useState("");

  useEffect(() => {
    void getHealth()
      .then((h) => {
        setLabel(h.status === "ok" ? "Connected" : "Degraded");
        setKind(h.status === "ok" ? "ok" : "bad");
      })
      .catch(() => {
        setLabel("Unreachable");
        setKind("bad");
      });
  }, []);

  return (
    <div className="health">
      <span className={`dot ${kind}`} />
      {label}
    </div>
  );
}
