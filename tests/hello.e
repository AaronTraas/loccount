-- should count as 12 lines
indexing
    description: "Root for trivial system printing a message"
    author : " Elizabeth W. Brown "
class HELLO create
    make
feature
    make is
            -- Print a simple message.
         do
            io . put_string (" Hello World ")
            io . put_new_line
         end
end -- class HELLO
