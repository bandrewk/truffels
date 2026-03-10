/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        surface: {
          DEFAULT: '#0f1117',
          raised: '#161923',
          overlay: '#1c1f2e',
        },
        border: {
          DEFAULT: '#2a2d3e',
          subtle: '#1f2233',
        },
        accent: {
          DEFAULT: '#f7931a',
          hover: '#ffa940',
          dim: '#f7931a20',
        },
      },
    },
  },
  plugins: [],
}
