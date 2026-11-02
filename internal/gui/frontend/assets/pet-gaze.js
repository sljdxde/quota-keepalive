export function petAimPoint(clientX, clientY, stageRect, avatarRect) {
  if (!stageRect || !avatarRect ||
      !Number.isFinite(clientX) || !Number.isFinite(clientY) ||
      stageRect.width <= 0 || stageRect.height <= 0 ||
      avatarRect.width <= 0 || avatarRect.height <= 0) {
    return null;
  }
  if (clientX < stageRect.left || clientX > stageRect.right ||
      clientY < stageRect.top || clientY > stageRect.bottom) {
    return null;
  }
  const headX = avatarRect.left - stageRect.left + avatarRect.width * 0.5;
  const headY = avatarRect.top - stageRect.top + avatarRect.height * 0.42;
  const x = clientX - stageRect.left;
  const y = clientY - stageRect.top;
  return {
    x: Math.max(-1, Math.min(1,
      (x - headX) / Math.max(56, stageRect.width * 0.38))),
    y: Math.max(-1, Math.min(1,
      (y - headY) / Math.max(44, stageRect.height * 0.5))),
  };
}
