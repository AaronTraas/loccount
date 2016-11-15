foreach number (one two three exit four)
  if ($number == exit) then
    echo reached an exit
    # Random commewnd as interruption
    continue
  endif
  echo $number
end
# Should count as 7 lines
