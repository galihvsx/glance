import { useAuth } from "../lib/auth";
import { Button } from "../components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card";

// Placeholder home — real workspace/project/issue pages arrive in Phase 2+.
export default function Home() {
  const { user, logout } = useAuth();

  return (
    <div className="flex min-h-svh items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>glance</CardTitle>
          <CardDescription>
            Signed in as {user?.name ?? user?.email}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">
            The workspace home lands in Phase 2. For now, auth works end to
            end.
          </p>
          <Button
            variant="outline"
            className="mt-4 w-full"
            onClick={() => void logout()}
          >
            Log out
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}
