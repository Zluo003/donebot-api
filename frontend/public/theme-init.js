(() => {
  let theme = "";
  try {
    const stored = localStorage.getItem("donebot-site-theme");
    if (stored === "light" || stored === "dark") theme = stored;
  } catch {
  }
  if (!theme) theme = window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  document.documentElement.dataset.theme = theme;
  const themeColor = document.querySelector('meta[name="theme-color"]');
  if (themeColor) themeColor.content = theme === "dark" ? "#000000" : "#ffffff";
})();
