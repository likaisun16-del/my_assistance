import com.theokanning.openai.completion.chat.ChatCompletionRequest;
import com.theokanning.openai.completion.chat.ChatMessage;
import com.theokanning.openai.completion.chat.ChatMessageRole;
import com.theokanning.openai.service.OpenAiService;

import java.util.Arrays;

public class Main {
    public static void main(String[] args) {
        OpenAiService service = new OpenAiService("your-api-key");

        String prompt = """
你是一个音乐分析助手
输入：一首歌
输出：
{
  "bpm": "",
  "情绪": "",
  "风格": "",
  "结构": ""
}
输入：Yesterday by The Beatles
""";

        ChatCompletionRequest request = ChatCompletionRequest.builder()
                .model("gpt-3.5-turbo")
                .messages(Arrays.asList(new ChatMessage(ChatMessageRole.USER.value(), prompt)))
                .build();

        var response = service.createChatCompletion(request);
        System.out.println(response.getChoices().get(0).getMessage().getContent());
    }
}