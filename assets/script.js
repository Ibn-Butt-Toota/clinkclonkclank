const audioOptions = {
    channelCount: 1,
    autoGainControl: true,
    echoCancellation: true,
    noiseSuppression: true,
}

async function getMic() {
    try {
        return await navigator.mediaDevices.getUserMedia({ audio: audioOptions });
    } catch (error) {
        switch (error.name) {
            case "NotFoundError":
                console.error("No microphone found. Please ensure you have an audio input device connected.", error);
                break;
            case "NotReadableError":
                console.error("A hardware error occured at the operating system, browser, or Web page level which prevented access to the device.", error);
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

async function startWhipStream(userMedia) {
    try {
        const pc = new RTCPeerConnection({ iceServers: [{ urls: "stun:stun.l.google.com:19302" }] });

        pc.oniceconnectionstatechange = () => {
            if (pc.iceConnectionState === 'connected' || pc.iceConnectionState === 'completed') {
                setConnectionStatus(true);
            } else if (pc.iceConnectionState === 'disconnected' || pc.iceConnectionState === 'failed') {
                setConnectionStatus(false);
            }
        };

        pc.addEventListener("icecandidate", async ({ candidate }) => {
            if (candidate !== null) {
                return;
            }

            try {
                // null ice candidate means we're at the end of the candidate list
                const offer = pc.localDescription;
                const answer = await fetch("http://127.0.0.1:8080/whip", {
                    method: 'POST',
                    body: offer.sdp,
                    headers: {
                        Authorization: `Bearer chicken`,
                        'Content-Type': 'application/sdp'
                    }
                });

                // in the future if implementing retry, change this to `console.error()` and log the status code.
                if (!answer.ok) {
                    return;
                }

                const answerSDP = await answer.text();
                await pc.setRemoteDescription({ type: "answer", sdp: answerSDP });
            } catch (error) {
                console.error("WHIP exchange failed", error);
            }
        });

        for (const track of userMedia.getTracks()) {
            pc.addTrack(track);
        }

        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);

        return pc;
    } catch (error) {
        console.error("Failed to set up the peer connection.", error);
        return null;
    }
}

function setConnectionStatus(connected) {
    const status = document.querySelector("#status");
    if (!status) {
        return;
    }

    status.textContent = connected ? "Connected" : "Disconnected";
    status.style.color = connected ? "green" : "red";
}

(() => {
    const micButton = document.querySelector("#mic");
    if (!micButton) {
        console.log("Mic button not found");
        return;
    }

    let pc = null;

    micButton.addEventListener("click", async () => {
        const userMedia = await getMic();
        if (!userMedia) {
            return;
        }

        pc = await startWhipStream(userMedia);
    });
})()
