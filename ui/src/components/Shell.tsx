// The signed-in shell: FI-branded header (wordmark, company, notifications, user menu), left navigation, and
// footer. Customers get the commercial-banking navigation; bank staff (Company ID BANK) get the operations shell.
import { useEffect, useRef, useState } from "react";
import { NavLink, Navigate, Outlet, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  AlertTriangle, Bell, Building2, ChevronDown, CircleUser, FileBarChart, Home, Landmark, LogOut, Mail, Send,
  Settings, ShieldCheck, Upload, Wallet,
} from "lucide-react";
import { api } from "../api";
import { useAnnotations } from "../annotations";
import { useBranding, Wordmark } from "../branding";
import { session } from "../session";

function Disabled({ icon, label, sub }: { icon: React.ReactNode; label: string; sub?: boolean }) {
  return (
    <span className={`disabled ${sub ? "sub" : ""}`} title="Not part of this demo" aria-disabled="true">
      {icon} {label}
    </span>
  );
}

export function Shell() {
  const branding = useBranding();
  const user = session.user();
  const navigate = useNavigate();
  const [menuOpen, setMenuOpen] = useState(false);
  const [annotations, setAnnotations] = useAnnotations();
  const menuRef = useRef<HTMLDivElement>(null);

  const approvals = useQuery({
    queryKey: ["batches", "approvals"],
    queryFn: () => api.batches("approvals"),
    enabled: user?.role === "Approver",
    refetchInterval: 5000,
  });

  useEffect(() => {
    const close = (e: MouseEvent) => menuRef.current && !menuRef.current.contains(e.target as Node) && setMenuOpen(false);
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, []);

  if (!user || !session.token()) return <Navigate to="/login" replace />;
  const ops = user.role === "BankOps";

  const signOut = () => {
    session.signOut();
    navigate("/login");
  };

  return (
    <>
      <header className="header">
        <Wordmark branding={branding} />
        <span className="company">{ops ? "Bank Operations" : user.companyName}</span>
        <span className="spacer" />
        <button className="icon" aria-label="Notifications (not part of this demo)" title="Not part of this demo">
          <Bell size={20} />
        </button>
        <div className="user-menu" ref={menuRef}>
          <button className="trigger" aria-haspopup="menu" aria-expanded={menuOpen} onClick={() => setMenuOpen((o) => !o)}>
            <CircleUser size={20} /> {user.name} · {ops ? branding.displayName : user.companyName} <ChevronDown size={16} />
          </button>
          {menuOpen && (
            <div className="menu" role="menu">
              <button disabled title="Not part of this demo"><CircleUser size={16} /> Profile</button>
              <div className="heading">Demo</div>
              <label>
                <input type="checkbox" checked={annotations} onChange={(e) => setAnnotations(e.target.checked)} />
                Show engineering annotations
              </label>
              <button onClick={signOut}><LogOut size={16} /> Sign out</button>
            </div>
          )}
        </div>
      </header>
      <div className="layout">
        <nav className="nav" aria-label="Main">
          {ops ? (
            <>
              <NavLink to="/ops/exceptions"><AlertTriangle size={18} /> Bulk wire exceptions</NavLink>
              <NavLink to="/ops/accounts"><Building2 size={18} /> Accounts</NavLink>
              <Disabled icon={<FileBarChart size={18} />} label="Reports" />
              <Disabled icon={<Settings size={18} />} label="Settings" />
            </>
          ) : (
            <>
              <NavLink to="/" end><Home size={18} /> Home</NavLink>
              <Disabled icon={<Wallet size={18} />} label="Accounts" />
              <span className="group"><Send size={18} /> Payments</span>
              <Disabled icon={<Landmark size={16} />} label="Wires" sub />
              <Disabled icon={<Landmark size={16} />} label="ACH" sub />
              <NavLink to="/payments/bulk-wire" className={({ isActive }) => `sub ${isActive ? "active" : ""}`}>
                <Upload size={16} /> Bulk Wire Upload
              </NavLink>
              <NavLink to="/approvals">
                <ShieldCheck size={18} /> Approvals
                {(approvals.data?.length ?? 0) > 0 && <span className="badge">{approvals.data!.length}</span>}
              </NavLink>
              <Disabled icon={<FileBarChart size={18} />} label="Reports" />
              <Disabled icon={<Mail size={18} />} label="Messages" />
              <Disabled icon={<Settings size={18} />} label="Settings" />
            </>
          )}
        </nav>
        <main className="main">
          <Outlet />
        </main>
      </div>
      <footer className="footer">© 2026 {branding.displayName}. Fictional institution for demonstration purposes.</footer>
    </>
  );
}
