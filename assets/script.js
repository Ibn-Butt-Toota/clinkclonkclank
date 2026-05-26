const audioOptions = {
    channelCount: 1,
    autoGainControl: true,
    echoCancellation: true,
    noiseSuppression: true,
}

async function getMic() {
    try {
        const userMedia = await navigator.mediaDevices.getUserMedia({ audio: audioOptions });
        const peerConnection = new RTCPeerConnection({ iceServers: [{ urls: "stun:stun.l.google.com:19302" }] });
        peerConnection.addEventListener("icecandidate", async ({candidate}) => {
            if (candidate !== null) {
                return;
            }

            // null ice candidate means we're at the end of the candidate list
            const offer = peerConnection.localDescription;
            const answer = await fetch("/whip", {
                method: 'POST',
                body: offer.sdp,
                headers: {
                    Authorization: `Bearer test`,
                    'Content-Type': 'application/sdp'
                }
            });

            // in the future if implementing retry, change this to `console.error()` and log the status code.
            if (!answer.ok) {
                return;
            }

            const answerSDP = await answer.text();
            peerConnection.setRemoteDescription(answerSDP);
        });

        for (const track of userMedia.getTracks()) {
            peerConnection.addTrack(track);
        }

        let offer = await peerConnection.createOffer();
        peerConnection.setLocalDescription(offer);

        return;
    } catch (error) {
        switch (error.name) {
            case "NotFoundError":
                console.error("No microphone found. Please ensure you have an audio input device connected.", error);
                break;
            case "NotReadableError":
                console.error("A hardware error occured at the operating system, browser, or Web page level which prevented access to the device.", error)
                break;
            case "SecurityError":
                console.error("The user media support is disabled on the page.", error);
                break;
            default:
                console.error("There was an error getting the user mic. See the error for more info.", error);
        }
        return null;
    }
}

(() => {
    const micButton = document.querySelector("#mic");
    if (!micButton) {
        console.log("Mic button not found");
        return;
    }

    validMic = micButton.addEventListener("click", getMic);
    if (!validMic) {
        return;
    }
})()
