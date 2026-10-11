// Account menu for the shell (C19T1).
//
// The prototype's shell has no sidebar footer: the user avatar lives in
// the topbar right cluster and opens this account dropdown (Profile,
// API tokens, Administration for admins, Log out). The old sidebar
// footer row (avatar + theme toggle + PWA install) was removed with the
// shadcn sidebar; ThemeToggle remains available on other surfaces
// (e.g. the notifications bell).

import { useNavigate } from "react-router-dom";
import type { ReactElement } from "react";
import { KeyRound, LogOut, ShieldCheck, User } from "lucide-react";
import { useAuth } from "../../lib/auth";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";

export function AccountMenu({ trigger }: { trigger: ReactElement }) {
  const navigate = useNavigate();
  const { user, logout } = useAuth();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={trigger} />
      <DropdownMenuContent align="end" side="bottom" className="w-56">
        <DropdownMenuItem onClick={() => navigate("/profile")}>
          <User className="h-4 w-4" />
          Profile
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => navigate("/settings/tokens")}>
          <KeyRound className="h-4 w-4" />
          API tokens
        </DropdownMenuItem>
        {user?.is_admin && (
          <DropdownMenuItem onClick={() => navigate("/admin")}>
            <ShieldCheck className="h-4 w-4" />
            Administration
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => void logout()}>
          <LogOut className="h-4 w-4" />
          Log out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
