"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { clearSession, logout, me } from "./api";

export function SessionBar() {
  const pathname = usePathname();
  const router = useRouter();
  const [name, setName] = useState("");

  useEffect(() => {
    if (pathname.startsWith("/login")) {
      return;
    }
    void me()
      .then((u) => setName(u.username))
      .catch(() => setName(""));
  }, [pathname]);

  if (pathname.startsWith("/login") || !name) {
    return null;
  }

  async function onLogout() {
    try {
      await logout();
    } catch {
      // Cookie/session may already be gone.
    }
    clearSession();
    router.replace("/login");
  }

  return (
    <div className="session-bar">
      <Link href="/observe" className="session-name">
        Observe
      </Link>
      <span className="session-name">{name}</span>
      <button className="btn-ghost" type="button" onClick={() => void onLogout()}>
        Log out
      </button>
    </div>
  );
}
