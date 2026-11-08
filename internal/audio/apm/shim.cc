// Thin C wrapper around webrtc::AudioProcessing (webrtc-audio-processing-2).
#include "shim.h"

#include <modules/audio_processing/include/audio_processing.h>

struct murmel_apm {
  rtc::scoped_refptr<webrtc::AudioProcessing> apm;
  webrtc::StreamConfig stream{48000, 1};
};

extern "C" {

murmel_apm *murmel_apm_new(void) {
  rtc::scoped_refptr<webrtc::AudioProcessing> apm =
      webrtc::AudioProcessingBuilder().Create();
  if (!apm) {
    return nullptr;
  }
  murmel_apm *p = new murmel_apm();
  p->apm = apm;
  return p;
}

void murmel_apm_free(murmel_apm *p) { delete p; }

void murmel_apm_configure(murmel_apm *p, int noise_level, int high_pass,
                          int echo_cancel) {
  webrtc::AudioProcessing::Config cfg;
  cfg.high_pass_filter.enabled = high_pass != 0;
  cfg.noise_suppression.enabled = noise_level >= 0;
  switch (noise_level) {
    case 0:
      cfg.noise_suppression.level =
          webrtc::AudioProcessing::Config::NoiseSuppression::kLow;
      break;
    case 2:
      cfg.noise_suppression.level =
          webrtc::AudioProcessing::Config::NoiseSuppression::kHigh;
      break;
    case 3:
      cfg.noise_suppression.level =
          webrtc::AudioProcessing::Config::NoiseSuppression::kVeryHigh;
      break;
    default:
      cfg.noise_suppression.level =
          webrtc::AudioProcessing::Config::NoiseSuppression::kModerate;
  }
  cfg.echo_canceller.enabled = echo_cancel != 0;
  p->apm->ApplyConfig(cfg);
}

int murmel_apm_process(murmel_apm *p, int16_t *frame) {
  return p->apm->ProcessStream(frame, p->stream, p->stream, frame);
}

int murmel_apm_process_reverse(murmel_apm *p, int16_t *frame) {
  return p->apm->ProcessReverseStream(frame, p->stream, p->stream, frame);
}

int murmel_apm_set_delay(murmel_apm *p, int delay_ms) {
  return p->apm->set_stream_delay_ms(delay_ms);
}

}  // extern "C"
