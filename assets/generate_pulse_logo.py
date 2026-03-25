"""
Generate logo_animated_pulse.svg — a variant of logo_animated.svg that adds
a single scale pulse on load, then the beam sweeps continuously.

Animation timeline:
  0.0s–0.8s  Pulse: logo scales 1.0 → 1.08 → 1.0 (once only)
  0.6s onward Beam sweep loops with idle gaps (same as original)
  Glow + opacity breathe continuously
"""

import re
import os

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
INPUT_PATH = os.path.join(SCRIPT_DIR, "logo_animated.svg")
OUTPUT_PATH = os.path.join(SCRIPT_DIR, "logo_animated_pulse.svg")

TOTAL_DUR = "5s"
PULSE_DUR = "0.8s"
BEAM_BEGIN = "0.6s"
BEAM_DUR = "1.8s"
IDLE_AFTER_BEAM = "2.6s"  # time from beam end to next loop


def extract_image_data(svg_content: str) -> str:
    """Extract the base64 image data URI from the SVG."""
    match = re.search(r'href="(data:image/png;base64,[^"]+)"', svg_content)
    if not match:
        raise ValueError("Could not find base64 image data in SVG")
    return match.group(1)


def build_svg(image_data: str) -> str:
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 830 349" width="830" height="349">
  <defs>
    <!-- Glow filter -->
    <filter id="glow" x="-20%" y="-20%" width="140%" height="140%">
      <feGaussianBlur in="SourceGraphic" stdDeviation="0" result="blur">
        <animate attributeName="stdDeviation" values="4;14;4" dur="3s" repeatCount="indefinite" calcMode="spline" keySplines="0.42 0 0.58 1;0.42 0 0.58 1"/>
      </feGaussianBlur>
      <feColorMatrix in="blur" type="matrix"
        values="1.5 0 0 0 0
                0 1.5 0 0 0
                0 0 1.5 0 0
                0 0 0 0.7 0" result="brightBlur"/>
      <feMerge>
        <feMergeNode in="brightBlur"/>
        <feMergeNode in="SourceGraphic"/>
      </feMerge>
    </filter>

    <!-- Shine gradient -->
    <linearGradient id="shineGrad" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0%" stop-color="white" stop-opacity="0"/>
      <stop offset="30%" stop-color="white" stop-opacity="0.75"/>
      <stop offset="50%" stop-color="white" stop-opacity="1"/>
      <stop offset="70%" stop-color="white" stop-opacity="0.75"/>
      <stop offset="100%" stop-color="white" stop-opacity="0"/>
    </linearGradient>
  </defs>

  <!-- Pulse + beam cycle: pulse → beam → idle → repeat -->
  <g transform-origin="415 174.5">
    <animateTransform
      attributeName="transform"
      type="scale"
      values="1;1.08;1"
      keyTimes="0;0.5;1"
      dur="{PULSE_DUR}"
      calcMode="spline"
      keySplines="0.42 0 0.58 1;0.42 0 0.58 1"
      repeatCount="1"
      begin="0s;beamRestart.end"
      id="pulse"
      fill="freeze"/>

    <!-- Logo with glow -->
    <image href="{image_data}"
      x="0" y="0" width="830" height="349"
      filter="url(#glow)" image-rendering="optimizeQuality">
      <animate attributeName="opacity" values="0.92;1;0.92" dur="3s" repeatCount="indefinite" calcMode="spline" keySplines="0.42 0 0.58 1;0.42 0 0.58 1"/>
    </image>

    <!-- Shine beam masked to logo shape -->
    <g style="mix-blend-mode: screen;">
      <mask id="logoMask">
        <image href="{image_data}"
          x="0" y="0" width="830" height="349"/>
      </mask>
      <g mask="url(#logoMask)">
        <rect x="-300" y="-20" width="300" height="390" fill="url(#shineGrad)" opacity="1">
          <animateTransform
            attributeName="transform"
            type="translate"
            from="-300,0"
            to="1150,0"
            dur="{BEAM_DUR}"
            calcMode="spline"
            keySplines="0.25 0.1 0.25 1"
            begin="pulse.end"
            id="beam"
            fill="freeze"/>
          <!-- Timer for the idle gap between beam sweeps -->
          <animate
            attributeName="visibility"
            from="visible"
            to="visible"
            begin="beam.end"
            dur="{IDLE_AFTER_BEAM}"
            id="beamRestart"
            fill="freeze"/>
        </rect>
      </g>
    </g>
  </g>
</svg>'''


def main():
    print(f"Reading {INPUT_PATH}...")
    with open(INPUT_PATH, "r") as f:
        svg_content = f.read()

    image_data = extract_image_data(svg_content)
    print(f"Extracted image data ({len(image_data)} chars)")

    svg = build_svg(image_data)

    with open(OUTPUT_PATH, "w") as f:
        f.write(svg)

    size_kb = os.path.getsize(OUTPUT_PATH) / 1024
    print(f"Written {OUTPUT_PATH} ({size_kb:.1f} KB)")


if __name__ == "__main__":
    main()
