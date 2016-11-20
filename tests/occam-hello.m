-- occam
PROC write.string(CHAN output, VALUE string[])=
  SEQ character.number = [1 FOR string[BYTE 0]]
    output ! string[BYTE character.number]
    
write.string(terminal.screen, "Hello World!")
