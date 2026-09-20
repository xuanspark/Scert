export function placeTooltip(anchor, popup, viewport) {
  const padding = 12;
  const gap = 8;
  const maxLeft = Math.max(padding, viewport.width - popup.width - padding);
  const left = Math.max(padding, Math.min(anchor.left, maxLeft));
  const below = anchor.bottom + gap;
  const above = anchor.top - popup.height - gap;
  const preferred = below + popup.height <= viewport.height - padding ? below : above;
  const maxTop = Math.max(padding, viewport.height - popup.height - padding);
  return {
    left,
    top: Math.max(padding, Math.min(preferred, maxTop)),
  };
}
