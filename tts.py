from kittentts import KittenTTS
# from kittentts import normalize_text

import soundfile as sf


def main():
    m = KittenTTS("KittenML/kitten-tts-mini-0.8")

    with open("transcript.txt", "r") as f:
        transcript = f.read()

    # normalize_text isn't released yet but it seems like it will be available in the next release.
    # norm_text = normalize_text(transcript)

    # available_voices : ['Bella', 'Jasper', 'Luna', 'Bruno', 'Rosie', 'Hugo', 'Kiki', 'Leo']
    audio = m.generate(transcript, voice='Hugo', speed=1.2)

    # Save the audio
    sf.write('tts.ogg', audio, 24000, "opus")


if __name__ == "__main__":
    main()