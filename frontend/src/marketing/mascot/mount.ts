import { createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { CursorAvatar, type CursorState } from "./CursorAvatar";
import { MASCOT_BODIES } from "./bodies";
import { avatarGradient } from "./colors";
import { mountHeroMotion } from "./hero-motion";
const roots = new Map<HTMLElement, Root>();
let heroMounted = false;
export function mountMascots(container: ParentNode = document) {
  if (!heroMounted) {
    mountHeroMotion();
    heroMounted = true;
  }
  container
    .querySelectorAll<HTMLElement>("[data-mascot]")
    .forEach((element) => {
      let root = roots.get(element);
      if (!root) {
        root = createRoot(element);
        roots.set(element, root);
      }
      root.render(
        createElement(CursorAvatar, {
          size: "100%",
          title: null,
          state: (element.dataset.mascot || "idle") as CursorState,
          silhouette:
            MASCOT_BODIES[element.dataset.body as keyof typeof MASCOT_BODIES] ??
            MASCOT_BODIES.circle,
          gradient: avatarGradient(element.dataset.color || "yellow"),
          eyeColor: element.dataset.color === "chrome" ? "#1E2833" : "#ffffff",
          effects: false,
          glyphs: false,
          lookAround: 0.35,
        }),
      );
    });
}
