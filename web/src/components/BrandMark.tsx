import { useId } from 'react';

interface Props {
  size?: number;
  className?: string;
}

/** Inline SVG brand mark: rounded gradient tile with a stylized "YZ" stroke. */
export default function BrandMark({ size = 32, className }: Props) {
  const gid = `yz-brand-g-${useId().replace(/[^a-zA-Z0-9]/g, '')}`;
  return (
    <svg
      className={className}
      width={size}
      height={size}
      viewBox="0 0 64 64"
      xmlns="http://www.w3.org/2000/svg"
      aria-label="YZ AI Gateway"
    >
      <defs>
        <linearGradient id={gid} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#6366f1" />
          <stop offset="1" stopColor="#06b6d4" />
        </linearGradient>
      </defs>
      <rect width="64" height="64" rx="14" fill={`url(#${gid})`} />
      <path
        d="M18 18l14 16v12M46 18L32 34"
        stroke="#fff"
        strokeWidth="6"
        strokeLinecap="round"
        strokeLinejoin="round"
        fill="none"
      />
    </svg>
  );
}
