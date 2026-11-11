/* Shortcut notation: the accelerator string the backend stores on one side,
   the key caps the interface shows on the other. Split from main.ts when the
   size ledger asked for a module rather than another budget raise (#30's
   round); it depends on nothing in the page but which platform is running. */

export const DEFAULT_SHORTCUT = "CmdOrCtrl+Alt+S";

let isMac = navigator.platform.toLowerCase().includes("mac");

/* The runtime knows the platform better than navigator.platform does; main.ts
   calls this once MyGo answers, and before the first label is drawn. */
export function setShortcutPlatform(mac: boolean) {
  isMac = mac;
}

export function formatShortcut(accelerator: string): string {
  const value = (accelerator || DEFAULT_SHORTCUT).trim();
  const parts = value.split("+").map((part) => part.trim()).filter(Boolean);
  const key = parts.pop() ?? "";
  const mods = parts.map((part) => {
    switch (part.toLowerCase()) {
      case "cmdorctrl":
      case "commandorcontrol":
        return isMac ? "⌘" : "Ctrl";
      case "cmd":
      case "command":
      case "super":
      case "meta":
      case "win":
        return isMac ? "⌘" : "Win";
      case "ctrl":
      case "control":
        return isMac ? "⌃" : "Ctrl";
      case "alt":
      case "option":
      case "altgr":
        return isMac ? "⌥" : "Alt";
      case "shift":
        return isMac ? "⇧" : "Shift";
      default:
        return part;
    }
  });
  const label = shortcutKeyLabel(key);
  return isMac ? [...mods, label].join("") : [...mods, label].join("+");
}

function shortcutKeyLabel(key: string): string {
  const named: Record<string, string> = isMac
    ? { Space: "Space", Enter: "↩", Tab: "⇥", Backspace: "⌫", Delete: "⌦", Up: "↑", Down: "↓", Left: "←", Right: "→", Escape: "⎋", PrintScreen: "PrtSc" }
    : { Space: "Space", Enter: "Enter", Tab: "Tab", Backspace: "Backspace", Delete: "Delete", Up: "Up", Down: "Down", Left: "Left", Right: "Right", Escape: "Esc", PrintScreen: "PrtSc" };
  return named[key] ?? (key.length === 1 ? key.toUpperCase() : key);
}

export function acceleratorFromEvent(event: KeyboardEvent): string | null {
  const mods: string[] = [];
  if (event.metaKey) mods.push("Cmd");
  if (event.ctrlKey) mods.push("Ctrl");
  if (event.altKey) mods.push("Alt");
  if (event.shiftKey) mods.push("Shift");
  if (mods.length === 0) return null;
  const key = acceleratorKey(event);
  if (!key) return null;
  return [...mods, key].join("+");
}

function acceleratorKey(event: KeyboardEvent): string | null {
  const code = event.code;
  if (/^Key[A-Z]$/.test(code)) return code.slice(3);
  if (/^Digit[0-9]$/.test(code)) return code.slice(5);
  if (/^F([1-9]|1[0-9]|2[0-4])$/.test(code)) return code;
  const named: Record<string, string> = {
    Space: "Space",
    Enter: "Enter",
    NumpadEnter: "Enter",
    Tab: "Tab",
    Backspace: "Backspace",
    Delete: "Delete",
    Insert: "Insert",
    ArrowUp: "Up",
    ArrowDown: "Down",
    ArrowLeft: "Left",
    ArrowRight: "Right",
    Home: "Home",
    End: "End",
    PageUp: "PageUp",
    PageDown: "PageDown",
    PrintScreen: "PrintScreen",
    Minus: "-",
    Equal: "=",
    BracketLeft: "[",
    BracketRight: "]",
    Backslash: "\\",
    Semicolon: ";",
    Quote: "'",
    Comma: ",",
    Period: ".",
    Slash: "/",
    Backquote: "`",
  };
  return named[code] ?? null;
}

export function isReservedShortcut(accelerator: string): boolean {
  const reserved = isMac
    ? ["Cmd+Q", "Cmd+W", "Cmd+Tab", "Cmd+Space", "Cmd+H", "Cmd+M", "Cmd+Shift+3", "Cmd+Shift+4", "Cmd+Shift+5"]
    : ["Ctrl+Alt+Delete"];
  return reserved.includes(accelerator);
}
