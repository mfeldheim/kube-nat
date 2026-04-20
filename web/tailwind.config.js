/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      fontFamily: {
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'ui-monospace', 'SFMono-Regular', 'monospace'],
      },
      colors: {
        brand: {
          50:  '#eef4ff',
          100: '#dae6ff',
          200: '#b9ccff',
          300: '#8aa8ff',
          400: '#5f84ff',
          500: '#3b63ff',
          600: '#2547ea',
          700: '#1e3bc0',
          800: '#1c3499',
          900: '#14256b',
          950: '#0a1547',
        },
        ink: {
          950: '#05080f',
          900: '#0a0e1a',
          850: '#0e1424',
          800: '#131a2e',
          700: '#1c2540',
        },
      },
      boxShadow: {
        glow: '0 0 24px -6px rgba(96, 165, 250, 0.45)',
        'glow-green': '0 0 24px -6px rgba(52, 211, 153, 0.45)',
        'glow-red': '0 0 24px -6px rgba(239, 68, 68, 0.45)',
        card: '0 1px 0 0 rgba(255,255,255,0.04) inset, 0 24px 48px -24px rgba(0,0,0,0.6)',
      },
      backgroundImage: {
        'grid-fade':
          'radial-gradient(1200px 600px at 50% -10%, rgba(59,99,255,0.18), transparent 60%), radial-gradient(800px 400px at 100% 0%, rgba(52,211,153,0.08), transparent 60%)',
      },
      keyframes: {
        fadeUp: {
          '0%':   { opacity: '0', transform: 'translateY(6px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        fadeIn: {
          '0%':   { opacity: '0' },
          '100%': { opacity: '1' },
        },
        pulseDot: {
          '0%,100%': { opacity: '1', transform: 'scale(1)' },
          '50%':     { opacity: '0.55', transform: 'scale(1.35)' },
        },
        shimmer: {
          '0%':   { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' },
        },
        gradientShift: {
          '0%,100%': { backgroundPosition: '0% 50%' },
          '50%':     { backgroundPosition: '100% 50%' },
        },
      },
      animation: {
        'fade-up': 'fadeUp 0.5s ease-out both',
        'fade-in': 'fadeIn 0.4s ease-out both',
        'pulse-dot': 'pulseDot 2s ease-in-out infinite',
        shimmer: 'shimmer 2.4s linear infinite',
        'gradient-shift': 'gradientShift 8s ease-in-out infinite',
      },
    },
  },
  plugins: [],
}
