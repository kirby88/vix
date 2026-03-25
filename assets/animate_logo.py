"""
Animate the ViX logo with:
  1. Smooth pulsing glow on bright/purple areas
  2. Traveling lens-flare shine sweep across the logo
  3. Subtle hue/saturation oscillation
  4. 60 frames for buttery-smooth looping
"""

from PIL import Image, ImageEnhance, ImageFilter, ImageChops, ImageDraw
import numpy as np
import math
import os

# ── Config ──────────────────────────────────────────────────────────────
INPUT_PATH = os.path.join(os.path.dirname(__file__), "logo.png")
OUTPUT_PATH = os.path.join(os.path.dirname(__file__), "logo_animated.gif")

NUM_FRAMES = 60          # smooth loop
FRAME_DURATION_MS = 50   # 20 fps
LOOP_COUNT = 0           # infinite

# Glow
GLOW_MIN = 0.0
GLOW_MAX = 1.0
GLOW_BLUR_MIN = 4
GLOW_BLUR_MAX = 14
GLOW_BRIGHTNESS_BOOST = 0.35  # extra brightness at peak glow

# Shine sweep
SHINE_WIDTH = 0.18       # fraction of image width
SHINE_OPACITY = 0.45     # peak opacity of the shine band
SHINE_BLUR = 8

# Color pulse
HUE_SHIFT_DEG = 6        # subtle hue oscillation amplitude
SAT_BOOST_MAX = 0.12     # subtle saturation boost at peak


def ease_in_out(t: float) -> float:
    """Smooth ease-in-out (cubic)."""
    if t < 0.5:
        return 4 * t * t * t
    else:
        return 1 - (-2 * t + 2) ** 3 / 2


def make_glow_layer(img: Image.Image, strength: float) -> Image.Image:
    """Create a soft glow layer from bright areas of the image."""
    # Extract bright areas
    arr = np.array(img).astype(np.float32)
    # Luminance of RGB
    lum = 0.299 * arr[:, :, 0] + 0.587 * arr[:, :, 1] + 0.114 * arr[:, :, 2]
    # Mask: only pixels above threshold contribute to glow
    threshold = 120
    mask = np.clip((lum - threshold) / (255 - threshold), 0, 1)

    glow_arr = arr.copy()
    # Brighten the glow source
    boost = 1.0 + GLOW_BRIGHTNESS_BOOST * strength
    glow_arr[:, :, :3] = np.clip(glow_arr[:, :, :3] * boost, 0, 255)
    # Apply luminance mask to alpha for glow isolation
    glow_arr[:, :, 3] = glow_arr[:, :, 3] * mask * strength

    glow_img = Image.fromarray(glow_arr.astype(np.uint8), "RGBA")
    blur_r = GLOW_BLUR_MIN + (GLOW_BLUR_MAX - GLOW_BLUR_MIN) * strength
    glow_img = glow_img.filter(ImageFilter.GaussianBlur(radius=blur_r))
    return glow_img


def make_shine_band(width: int, height: int, center_x_frac: float) -> Image.Image:
    """Create a vertical shine band at a given horizontal position."""
    band = Image.new("RGBA", (width, height), (0, 0, 0, 0))
    draw = ImageDraw.Draw(band)

    band_w = int(width * SHINE_WIDTH)
    cx = int(center_x_frac * width)

    for dx in range(-band_w // 2, band_w // 2 + 1):
        x = cx + dx
        if x < 0 or x >= width:
            continue
        # Gaussian falloff from center of band
        dist = abs(dx) / (band_w / 2)
        alpha = int(255 * SHINE_OPACITY * math.exp(-4 * dist * dist))
        # White shine
        draw.line([(x, 0), (x, height - 1)], fill=(255, 255, 255, alpha))

    band = band.filter(ImageFilter.GaussianBlur(radius=SHINE_BLUR))
    return band


def shift_hue_sat(img: Image.Image, hue_shift_deg: float, sat_boost: float) -> Image.Image:
    """Shift hue and boost saturation slightly."""
    arr = np.array(img).astype(np.float32)
    r, g, b, a = arr[:, :, 0], arr[:, :, 1], arr[:, :, 2], arr[:, :, 3]

    # RGB -> HSV manually (vectorized)
    maxc = np.maximum(np.maximum(r, g), b)
    minc = np.minimum(np.minimum(r, g), b)
    diff = maxc - minc

    # Hue
    h = np.zeros_like(r)
    mask = diff > 0
    m_r = mask & (maxc == r)
    m_g = mask & (maxc == g) & ~m_r
    m_b = mask & ~m_r & ~m_g

    h[m_r] = (60 * ((g[m_r] - b[m_r]) / diff[m_r]) + 360) % 360
    h[m_g] = (60 * ((b[m_g] - r[m_g]) / diff[m_g]) + 120) % 360
    h[m_b] = (60 * ((r[m_b] - g[m_b]) / diff[m_b]) + 240) % 360

    # Saturation
    s = np.zeros_like(r)
    s[maxc > 0] = diff[maxc > 0] / maxc[maxc > 0]
    v = maxc

    # Apply shifts
    h = (h + hue_shift_deg) % 360
    s = np.clip(s * (1 + sat_boost), 0, 1)

    # HSV -> RGB
    h60 = h / 60.0
    hi = np.floor(h60).astype(int) % 6
    f = h60 - np.floor(h60)
    p = v * (1 - s)
    q = v * (1 - f * s)
    t_val = v * (1 - (1 - f) * s)

    out = np.zeros_like(arr)
    out[:, :, 3] = a

    for idx, (r_val, g_val, b_val) in enumerate([
        (v, t_val, p), (q, v, p), (p, v, t_val),
        (p, q, v), (t_val, p, v), (v, p, q)
    ]):
        m = hi == idx
        out[:, :, 0][m] = r_val[m]
        out[:, :, 1][m] = g_val[m]
        out[:, :, 2][m] = b_val[m]

    return Image.fromarray(np.clip(out, 0, 255).astype(np.uint8), "RGBA")


def generate_frames(img: Image.Image) -> list[Image.Image]:
    w, h = img.size
    frames = []

    for i in range(NUM_FRAMES):
        t = i / NUM_FRAMES  # 0..1 through the loop

        # ── Glow: smooth sine pulse ──
        glow_t = (math.sin(t * 2 * math.pi) + 1) / 2  # 0..1
        glow_strength = GLOW_MIN + (GLOW_MAX - GLOW_MIN) * ease_in_out(glow_t)

        # ── Shine: faster sweep left to right with pause ──
        # Complete the sweep in 60% of the loop, then stay off-screen
        shine_speed = 0.6  # fraction of loop for the sweep
        if t < shine_speed:
            sweep_t = t / shine_speed  # 0..1 during sweep
            shine_x = -SHINE_WIDTH + sweep_t * (1 + 2 * SHINE_WIDTH)
        else:
            shine_x = -SHINE_WIDTH - 1  # off-screen

        # ── Color: subtle hue oscillation (offset phase from glow) ──
        hue_shift = HUE_SHIFT_DEG * math.sin(t * 2 * math.pi + math.pi / 3)
        sat_boost = SAT_BOOST_MAX * ((math.sin(t * 2 * math.pi + math.pi / 2) + 1) / 2)

        # Build frame
        base = shift_hue_sat(img, hue_shift, sat_boost)
        glow = make_glow_layer(img, glow_strength)
        shine = make_shine_band(w, h, shine_x)

        # Composite: base + glow (screen-like via alpha_composite) + shine masked to logo
        frame = Image.alpha_composite(base, glow)

        # Mask shine to only show on opaque logo pixels
        logo_mask = img.split()[3]  # alpha channel
        shine_arr = np.array(shine)
        mask_arr = np.array(logo_mask).astype(np.float32) / 255.0
        shine_arr[:, :, 3] = (shine_arr[:, :, 3].astype(np.float32) * mask_arr).astype(np.uint8)
        shine_masked = Image.fromarray(shine_arr, "RGBA")

        frame = Image.alpha_composite(frame, shine_masked)
        frames.append(frame)

    return frames


def save_gif(frames: list[Image.Image], output_path: str):
    """Save frames as high-quality GIF with proper transparency handling."""
    # Convert RGBA frames to P mode with transparency for GIF
    # Use a consistent palette derived from the first frame
    gif_frames = []
    for frame in frames:
        # Composite onto black background for GIF (no transparency in output)
        bg = Image.new("RGBA", frame.size, (0, 0, 0, 255))
        composited = Image.alpha_composite(bg, frame)
        # Convert to RGB then quantize
        rgb = composited.convert("RGB")
        # Use high-quality quantization
        quantized = rgb.quantize(colors=256, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.FLOYDSTEINBERG)
        gif_frames.append(quantized)

    gif_frames[0].save(
        output_path,
        save_all=True,
        append_images=gif_frames[1:],
        duration=FRAME_DURATION_MS,
        loop=LOOP_COUNT,
        optimize=False,
    )


def main():
    print(f"Loading {INPUT_PATH}...")
    img = Image.open(INPUT_PATH).convert("RGBA")
    print(f"Image size: {img.size}")

    print(f"Generating {NUM_FRAMES} frames...")
    frames = generate_frames(img)

    print(f"Saving GIF to {OUTPUT_PATH}...")
    save_gif(frames, OUTPUT_PATH)

    size_mb = os.path.getsize(OUTPUT_PATH) / (1024 * 1024)
    print(f"Done! Output: {OUTPUT_PATH} ({size_mb:.1f} MB)")


if __name__ == "__main__":
    main()
