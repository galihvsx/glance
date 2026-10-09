import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "./ui/dialog";
import { Kbd, KbdGroup } from "./ui/kbd";
import {
  SHORTCUT_CONTEXTS,
  SHORTCUT_REGISTRY,
  type ShortcutDefinition,
} from "../lib/shortcuts";

function ShortcutKeys({ def }: { def: ShortcutDefinition }) {
  if (def.chord) {
    return (
      <KbdGroup>
        {def.keys.map((k, i) => (
          <span key={k} className="inline-flex items-center gap-1">
            {i > 0 && (
              <span className="text-xs text-muted-foreground">then</span>
            )}
            <Kbd>{k}</Kbd>
          </span>
        ))}
      </KbdGroup>
    );
  }
  return (
    <KbdGroup>
      {def.keys.map((k) => (
        <Kbd key={k}>{k}</Kbd>
      ))}
    </KbdGroup>
  );
}

export default function ShortcutCheatsheet({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
          <DialogDescription>
            Press <Kbd>?</Kbd> anywhere to open this cheatsheet.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-6">
          {SHORTCUT_CONTEXTS.map((context) => {
            const defs = SHORTCUT_REGISTRY.filter((d) =>
              d.contexts.includes(context),
            );
            if (defs.length === 0) return null;
            return (
              <section key={context}>
                <h3 className="mb-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                  {context}
                </h3>
                <dl className="divide-y divide-border rounded-md border border-border">
                  {defs.map((def) => (
                    <div
                      key={def.action}
                      className="flex items-center justify-between gap-4 px-3 py-2"
                    >
                      <dt className="text-sm">{def.description}</dt>
                      <dd className="shrink-0">
                        <ShortcutKeys def={def} />
                      </dd>
                    </div>
                  ))}
                </dl>
              </section>
            );
          })}
        </div>
      </DialogContent>
    </Dialog>
  );
}
