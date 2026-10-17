# Femi QA — Sat 17 Oct 23:06

## PASS voice-weather  (28.7 s, first response 0.0 s)

- ✓ Femi replied
- ✓ reply has audio (10.6 s)
- ✓ answer is about the weather
- Femi (24.7s, 10.6 s audio): "It's lightly raining in Bengaluru right now, about twenty-one degrees, though it feels a bit warmer because it's so humid. There's a good chance of more drizzle tonight, so it's a cozy rainy evening."

<details><summary>states</summary>

```
  0.0s thinking "Listening…"
  2.3s thinking "Hey Femi, what's the we…"
  7.3s working "Asking Fo…"
 20.3s thinking "Fo is done"
```
</details>

## PASS kid-learning  (10.6 s, first response 0.0 s)

- ✓ Femi replied
- ✓ talks about the moon
- Femi (6.6s, 9.8 s audio): "Hi Riya! Here's a fun one: the moon is slowly drifting away from us, about as far as your fingernail grows in a year. Did you know that? Do you want another moon fact?"

<details><summary>states</summary>

```
  0.0s thinking "Hi Femi, I'm Riya. Tell…"
```
</details>

## PASS timer  (11.5 s, first response 0.0 s)

- ✓ Femi replied
- ✓ timer command for 5 minutes
- Femi (7.5s, 3.4 s audio): "Okay, your five minute timer has started! I'll ring when it's done."
- command: {"minutes":5,"type":"timer"}

<details><summary>states</summary>

```
  0.0s thinking "Set a timer for 5 minut…"
```
</details>

## PASS memory  (9.5 s, first response 0.0 s)

- ✓ Femi replied
- ✓ remembers it was 5 minutes
- Femi (5.5s, 2.2 s audio): "Five minutes! It's already counting down."

<details><summary>states</summary>

```
  0.0s thinking "How many minutes was th…"
```
</details>

## PASS timer-cancel  (11.7 s, first response 0.0 s)

- ✓ Femi replied
- ✓ timer cancel command
- Femi (7.7s, 2.2 s audio): "All done, the timer is cancelled."
- command: {"action":"cancel","type":"timer"}

<details><summary>states</summary>

```
  0.0s thinking "Cancel the timer."
```
</details>

## PASS game  (11.4 s, first response 0.0 s)

- ✓ Femi replied
- ✓ starts the game app
- Femi (7.4s, 3.8 s audio): "Yay, the game is on your screen now! Give it a tap and have fun."
- command: {"name":"game","type":"app"}

<details><summary>states</summary>

```
  0.0s thinking "Let's play a game!"
```
</details>

## PASS morse  (11.8 s, first response 0.0 s)

- ✓ Femi replied
- ✓ starts the morse app
- Femi (7.8s, 5.3 s audio): "Morse is ready! A short tap on the pad makes a dot, and a longer press makes a dash."
- command: {"name":"morse","type":"app"}

<details><summary>states</summary>

```
  0.0s thinking "I want to practise Mors…"
```
</details>

## PASS theme  (11.6 s, first response 0.0 s)

- ✓ Femi replied
- ✓ hacker theme command
- ✓ normal theme command
- Femi (7.6s, 3.3 s audio): "Hacker mode is on! Your screen is now green on black."
- Femi (7.2s, 2.7 s audio): "Back to normal, my friendly face is back!"
- command: {"name":"hacker","type":"theme"}
- command: {"name":"normal","type":"theme"}

<details><summary>states</summary>

```
  0.0s thinking "Switch to hacker mode."
  0.0s thinking "Now go back to normal m…"
```
</details>

## PASS photos  (19.2 s, first response 0.0 s)

- ✓ Femi replied
- ✓ photos sent (2)
- ✓ photo pixels complete (307200 bytes)
- Femi (7.7s, 6.2 s audio): "Here they are! There's a lovely sunset at the beach, and a big happy smile. Aren't they pretty?"
- command: {"bytes":153600,"caption":"Sunset at the beach (test picture)","h":240,"type":"image","w":320}
- command: {"bytes":153600,"caption":"A big smile (test picture)","h":240,"type":"image","w":320}

<details><summary>states</summary>

```
  0.0s thinking "Show me the demo photos."
```
</details>

## PASS reminder  (4.0 s, first response 0.0 s)

- ✓ Femi replied
- ✓ reminder fired on the device
- ✓ acknowledging it makes Femi happy
- Femi (3.6s, 3.5 s audio): "All set! I'll remind you to drink a glass of water in one minute."
- Femi (63.0s, 2.3 s audio): "Reminder: Drink a glass of water."
- Femi (64.2s, 1.7 s audio): "Thank you, noted."
- command: {"id":"r1","text":"Drink a glass of water","type":"reminder","urgent":true}

<details><summary>states</summary>

```
  0.0s thinking "Remind me to drink a gl…"
 63.0s happy "Noted"
```
</details>

## PASS approval-declined  (13.4 s, first response 0.0 s)

- ✓ Femi replied
- ✓ asked for approval before emailing
- ✓ accepts the no
- approval card: "Email Rahul Sharma: dinner is at 8 tonight"
- Femi (9.4s, 3.8 s audio): "Okay, I won't send that email to Rahul. Let me know if you'd like to change it."
- command: {"id":"a1","text":"Email Rahul Sharma: dinner is at 8 tonight","type":"approve"}

<details><summary>states</summary>

```
  0.0s thinking "Email Rahul Sharma that…"
```
</details>

## PASS calendar-read  (30.8 s, first response 0.0 s)

- ✓ Femi replied
- ✓ reading needs no approval
- Femi (26.8s, 7.2 s audio): "I can't see the calendar just yet, because the Google account hasn't been connected. Once someone links it, ask me again and I'll check."

<details><summary>states</summary>

```
  0.0s thinking "What's on my calendar t…"
  4.1s working "Asking Fo…"
 23.2s thinking "Fo is done"
```
</details>

## PASS silence  (5.8 s, first response 0.0 s)

- ✓ Femi replied
- ✓ says it didn't catch that
- Femi (1.8s, 1.9 s audio): "Sorry, I didn't catch that."

<details><summary>states</summary>

```
  0.0s thinking "Listening…"
```
</details>

## PASS cancelled  (6.0 s, first response 0.0 s)

- ✓ a cancelled request is dropped silently

<details><summary>states</summary>

```
  0.0s idle ""
TIMEOUT
```
</details>

## PASS busy  (41.6 s, first response 0.0 s)

- ✓ Femi replied
- ✓ a second request while busy gets a polite wait
- Femi (0.9s, 2.4 s audio): "One moment, I'm still on the last thing."
- Femi (37.6s, 7.3 s audio): "Delhi is clear tonight, about twenty-three degrees, with a light breeze. It's a bit humid, but no rain is expected."

<details><summary>states</summary>

```
  0.0s thinking "What's the weather in D…"
  2.7s working "Asking Fo…"
 32.7s working "Waiting on Fo (30s)"
 34.4s thinking "Fo is done"
```
</details>

**15 passed, 0 failed**
