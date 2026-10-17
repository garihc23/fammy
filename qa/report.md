# Femi QA — Sat 17 Oct 19:48

## PASS kid-learning  (12.3 s, first response 0.0 s)

- ✓ Femi replied
- ✓ talks about the moon
- Femi (8.3s, 7.7 s audio): "Hi Riya! Here's a fun one: the moon is slowly drifting away from us, about as far as your fingernails grow in a year. Want another one?"

<details><summary>states</summary>

```
  0.0s thinking "Hi Femi, I'm Riya. Tell…"
```
</details>

## PASS timer  (11.5 s, first response 0.0 s)

- ✓ Femi replied
- ✓ timer command for 5 minutes
- Femi (7.5s, 3.3 s audio): "Your five minute timer is running, Riya. I'll ring when it's done!"
- command: {"minutes":5,"type":"timer"}

<details><summary>states</summary>

```
  0.0s thinking "Set a timer for 5 minut…"
```
</details>

## PASS morse  (12.3 s, first response 0.0 s)

- ✓ Femi replied
- ✓ starts the morse app
- Femi (8.3s, 5.4 s audio): "Morse is ready! Tap the touch pad: a short tap is a dot, and a longer press is a dash."
- command: {"name":"morse","type":"app"}

<details><summary>states</summary>

```
  0.0s thinking "I want to practise Mors…"
```
</details>

## PASS call  (52.7 s, first response 0.0 s)

- ✓ Femi replied
- ✓ asked for approval before calling
- ✓ showed Fo's progress while calling (1 updates)
- approval card: "Call +91 81717 64392: test call from Femi, all working"
- Femi (44.7s, 13.8 s audio): "The call is going out now. One small thing: the message will say it's from Fo, not Femi, so it will say \"this is a test call from Fo, the family assistant, and everything is working.\" I don't know yet if the call has finished."
- command: {"id":"a1","text":"Call +91 81717 64392: test call from Femi, all working","type":"approve"}

<details><summary>states</summary>

```
  0.0s thinking "Please call +91 81717 6…"
  8.1s working "Asking Fo…"
 37.3s thinking "Fo is done"
```
</details>

**4 passed, 0 failed**
