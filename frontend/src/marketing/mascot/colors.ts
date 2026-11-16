export const AVATAR_COLORS = {
  yellow: "#D8A729",
  cyan: "#0EA5C6",
  pink: "#D84F8B",
  orange: "#E78531",
  teal: "#01A492",
  chrome: "#B3BAC4",
} as const;

export type AvatarColor = keyof typeof AVATAR_COLORS;

function mix(hex: string, toward: string, t: number): string {
  const a = Number.parseInt(hex.slice(1), 16);
  const b = Number.parseInt(toward.slice(1), 16);
  return `#${[16, 8, 0]
    .map((shift) =>
      Math.round(
        ((a >> shift) & 255) +
          (((b >> shift) & 255) - ((a >> shift) & 255)) * t,
      )
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`;
}

export function avatarGradient(color: string): [string, string, string] {
  if (color === "chrome") return ["#F1F4F7", "#AEB8C3", "#66717E"];
  const fill = AVATAR_COLORS[color as AvatarColor] ?? AVATAR_COLORS.yellow;
  return [mix(fill, "#ffffff", 0.55), fill, mix(fill, "#000000", 0.42)];
}
