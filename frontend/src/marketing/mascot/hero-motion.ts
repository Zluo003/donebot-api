import { mascotClock, type MascotTicket } from "./mascot-clock";

export function mountHeroMotion() {
  const stage = document.querySelector<HTMLElement>(".hero-wordmark");
  if (!stage) return;
  const flyers = [...stage.querySelectorAll<HTMLElement>(".hero-flyer")];
  const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
  let width = stage.clientWidth;
  let height = stage.clientHeight;
  let elapsed = 0;
  let last = performance.now();
  let ticket: MascotTicket | undefined;
  const place = (time: number) => {
    flyers.forEach((flyer, i) => {
      const angle = (i / flyers.length) * Math.PI * 2 + time * 0.00013;
      const x = Math.cos(angle) * width * (0.35 + (i % 2) * 0.025);
      const y = Math.sin(angle) * height * (0.34 + (i % 2) * 0.04);
      const scale = 0.9 + Math.sin(angle) * 0.12;
      flyer.style.transform = `translate3d(${x.toFixed(2)}px, ${y.toFixed(2)}px, 0) translate(-50%, -50%) scale(${scale.toFixed(3)})`;
      flyer.style.zIndex = Math.sin(angle) > 0 ? "2" : "0";
    });
  };
  const resize = new ResizeObserver(() => {
    width = stage.clientWidth;
    height = stage.clientHeight;
    place(elapsed);
  });
  resize.observe(stage);
  const syncMotion = () => {
    ticket?.remove();
    ticket = undefined;
    last = performance.now();
    place(elapsed);
    if (reducedMotion.matches) return;
    ticket = mascotClock().add((now) => {
      elapsed += Math.min(now - last, 80);
      last = now;
      place(elapsed);
      return now + 1000 / 30;
    }, stage);
  };
  syncMotion();
  reducedMotion.addEventListener("change", syncMotion);
  window.addEventListener("pagehide", () => {
    ticket?.remove();
    resize.disconnect();
  });
  window.addEventListener("pageshow", (event) => {
    if (event.persisted) {
      resize.observe(stage);
      syncMotion();
    }
  });
}
