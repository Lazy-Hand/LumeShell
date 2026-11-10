import { defineConfig } from "mygo-cli";

// Native-UI app: no web frontend, no bindings, no dev server. The Go
// side draws every window with package ui.
export default defineConfig({
  name: "LumeShell",
  identifier: "com.example.lumeshell",
  version: "0.1.0",
  updates: {
    publicKey: "x0xLes3DR3ol6TG6uMVvj8jkqQ6iO1dUbynN7fcVyEs=",
    github: "Lazy-Hand/LumeShell",
  },
  out: "build",
});
