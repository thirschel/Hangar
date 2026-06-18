import soundUrl from './assets/notification.mp3';

// A single reused element avoids re-decoding the clip on every notification.
let audio: HTMLAudioElement | null = null;

// Plays the notification chime. Best-effort: autoplay restrictions, a missing
// audio device, or a still-loading clip must never bubble up and disrupt the UI.
export function playNotificationSound(): void {
  try {
    if (!audio) {
      audio = new Audio(soundUrl);
    }
    audio.currentTime = 0;
    void audio.play().catch(() => {});
  } catch {
    // ignore
  }
}
