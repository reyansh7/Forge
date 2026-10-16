// Forge mark: a hexagon (the platform) around an F monogram.
//
// The splash animates the same paths by stroke-dashoffset, so the top
// bar and the loading screen show one identity. pathLength={120}
// normalises every path to the same dash length so one CSS value
// (stroke-dasharray: 120) works for all of them.
export function LogoDefs() {
  return (
    <defs>
      <linearGradient id="forge-stroke" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0%" stopColor="#b0001a" />
        <stop offset="50%" stopColor="#e50914" />
        <stop offset="100%" stopColor="#ff4d5a" />
      </linearGradient>
      <linearGradient id="forge-fill" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0%" stopColor="#e50914" stopOpacity="0.3" />
        <stop offset="100%" stopColor="#ff4d5a" stopOpacity="0.06" />
      </linearGradient>
    </defs>
  );
}

export const HEX_PATH = "M32 4 L56 18 L56 46 L32 60 L8 46 L8 18 Z";
export const F_PATH = "M25 45 L25 20 L41 20 M25 32 L37 32";

export function Logo({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 64 64" aria-hidden="true">
      <defs>
        <linearGradient id="forge-logo-g" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor="#b0001a" />
          <stop offset="50%" stopColor="#e50914" />
          <stop offset="100%" stopColor="#ff4d5a" />
        </linearGradient>
      </defs>
      <path d={HEX_PATH} fill="rgba(229,9,20,0.14)" stroke="url(#forge-logo-g)" strokeWidth="3.4" strokeLinejoin="round" />
      <path d={F_PATH} fill="none" stroke="url(#forge-logo-g)" strokeWidth="4.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
