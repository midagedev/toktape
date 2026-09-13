package tui

// The words the example run's streams say.
//
// They are here rather than in example.go because of the length the run now
// needs (TTP-28, user 2026-09-13: "토큰 생성하는 화면을 충분히 살펴보기에 재생시간이
// 너무 짧아"). A stream that runs for twenty-five seconds at twelve tokens a
// second spends three hundred words, and a fixture that ran out of material
// looped back to its first sentence — which on screen reads as a model
// stuttering, and in a clip is the one thing a viewer is certain to notice.
// TestExampleStreamsNeverRepeatThemselves pins that: no eight-word run may
// occur twice inside one stream's answer and monologue together.
//
// What they say is what this tool's audience argues about: placement, the KV
// cache, batching, the prefix cache, page faults, quantisation, flash
// attention, speculative decoding. Two streams answer in Korean, which is not
// decoration — it is the case the layout has to survive, because a Hangul
// syllable is two columns wide (handover lesson 5).

// exampleAnswers is what each stream replies. Every one of them is longer than
// the 320-token budget a stream can spend, so no stream reaches the end of its
// own material.
var exampleAnswers = []string{
	// 0 — layer split across two cards, and why decode is memory-bound.
	"A dense seventy billion parameter model at Q4_K_M is about forty two " +
		"gigabytes of weights, and every single one of those bytes is read " +
		"again for every token you generate. That one sentence explains most " +
		"of what you are seeing. Decoding is not compute bound on this rig. " +
		"It is a memory bandwidth problem wearing a compute costume, and the " +
		"arithmetic is simple enough to do in your head. Take the bytes the " +
		"model has to touch per token, multiply by the tokens per second you " +
		"are getting, and compare the result against what your cards can " +
		"actually move. If the two figures are close, the placement is fine " +
		"and no flag is going to save you. If they are far apart, something " +
		"is stalling and it is worth finding out what. With a layer split " +
		"the first card holds the lower half of the network and the second " +
		"holds the upper half, so the two devices do not work at the same " +
		"time. The first one runs its layers while the second waits, then " +
		"the activations cross the bus, which costs almost nothing because " +
		"an activation is a few kilobytes, and the second card runs the rest " +
		"while the first waits. Each token therefore takes the sum of the " +
		"two halves rather than the maximum, which is why adding a card " +
		"raises the memory you can fill but not the speed at which one " +
		"session is answered. What it does raise is how many sessions you " +
		"can serve at once, and that is usually the thing people actually " +
		"want. Tensor parallelism would split each matrix instead of each " +
		"layer and let both devices work together on the same token, but it " +
		"wants a fast link between them and it is not what this server does " +
		"by default. So read the effective bandwidth figure as a report card " +
		"on placement, not as a speed limit you can argue with. Anything " +
		"that lowers the bytes per token lowers the time per token, which is " +
		"why a smaller quantisation feels faster even when the arithmetic " +
		"per token has not changed at all, and why offloading one layer to " +
		"the processor costs far more than its share of the weights suggests.",

	// 1 — KV 캐시와 컨텍스트 (Korean).
	"KV 캐시는 가중치와 완전히 다른 종류의 메모리다. 가중치는 모델을 올리는 " +
		"순간 크기가 정해지고 그 뒤로는 변하지 않지만, KV 캐시는 슬롯마다 " +
		"따로 잡히고 컨텍스트 길이에 정비례해서 자란다. 그래서 같은 모델을 " +
		"같은 카드에 올려도 컨텍스트를 네 배로 늘리면 갑자기 자리가 " +
		"모자란다. 계산은 어렵지 않다. 레이어 수에 키와 값 두 벌을 곱하고, " +
		"거기에 KV 헤드가 차지하는 채널 수와 컨텍스트 토큰 수를 곱한 다음, " +
		"원소 하나가 몇 바이트인지를 곱하면 된다. 팔십 레이어짜리 칠십억 " +
		"단위 모델이 만 육천 토큰을 잡으면 반정밀도로는 오 기가바이트가 " +
		"넘고, 캐시 타입을 여덟 비트로 내리면 절반을 조금 넘는 수준으로 " +
		"줄어든다. 이 절약분이 그대로 가중치를 올릴 공간이 되기 때문에, " +
		"카드가 빠듯할 때 가장 먼저 건드릴 손잡이가 캐시 타입이다. 품질 " +
		"이야기가 나오면 대개 과장돼 있다. 키와 값을 여덟 비트로 저장하는 " +
		"것은 가중치를 네 비트로 만드는 것과 성격이 다르다. 캐시는 이미 " +
		"계산이 끝난 중간 결과를 담아 두는 곳이고, 오차가 누적되는 경로가 " +
		"가중치보다 훨씬 짧다. 다만 키 쪽이 값 쪽보다 민감하다는 보고가 " +
		"꾸준히 있으니, 여유가 있다면 값만 먼저 내리고 키는 그대로 두는 " +
		"조합을 재 보는 편이 안전하다. 그리고 플래시 어텐션을 켜야 낮은 " +
		"정밀도의 캐시가 제값을 한다. 어텐션 커널이 캐시를 읽는 방식이 " +
		"달라서, 꺼 둔 채로 캐시만 양자화하면 지원되지 않거나 오히려 느려 " +
		"지는 경우가 있다. 여기서 한 가지 더 기억할 것은 슬롯 수다. " +
		"동시에 받을 요청 수를 여덟로 잡으면 컨텍스트도 여덟 몫으로 " +
		"나뉘거나, 설정에 따라서는 슬롯마다 전체 컨텍스트를 따로 잡는다. " +
		"어느 쪽인지 모르면 메모리 계산이 여덟 배 틀린다. 실행 로그에 " +
		"슬롯 하나가 몇 토큰을 갖는지가 찍히니 그 줄을 먼저 확인하고, " +
		"거기서 나온 숫자로 캐시 크기를 다시 계산해 보라. 화면 오른쪽의 " +
		"메모리 칸이 그 계산과 실제 장치 사용량을 나란히 보여 주는 이유도 " +
		"그것이다. 두 값이 어긋나면 설정이 생각과 다르게 잡혀 있다는 " +
		"뜻이고, 그때는 속도를 재기 전에 배치부터 고쳐야 한다. 실제로 " +
		"자리가 모자랄 때 손대는 순서도 정해 두면 편하다. 먼저 캐시 " +
		"타입을 여덟 비트로 내리고, 그래도 모자라면 컨텍스트를 줄이고, " +
		"그다음에 동시 슬롯 수를 줄인다. 가중치를 호스트로 내보내는 " +
		"선택은 가장 마지막이다. 레이어 하나를 프로세서로 보내는 순간 " +
		"토큰마다 그 레이어의 가중치가 버스를 건너야 하고, 그 비용은 " +
		"레이어가 차지하는 크기 비율보다 훨씬 크게 나타난다. 카드 두 " +
		"장에 나눠 담을 때 배분 비율도 한 번은 손으로 확인해 보는 게 " +
		"좋다. 기본값은 장치별 여유 메모리를 기준으로 나누는데, 화면 " +
		"출력을 담당하는 카드는 이미 얼마쯤 쓰고 있어서 같은 모델이라도 " +
		"양쪽이 균등하게 차지 않는다. 한쪽이 먼저 가득 차면 거기서 " +
		"할당이 실패하거나, 조용히 일부가 호스트로 밀려나서 속도가 " +
		"절반이 된다. 로그에 장치별로 몇 기가씩 잡혔는지 찍히니 그 줄과 " +
		"이 화면의 막대를 나란히 두고 보면 된다. 두 숫자가 맞으면 배치는 " +
		"끝난 것이고, 그때부터는 속도를 재도 좋다.",

	// 2 — batching and what concurrency really buys.
	"The number that changes when you send eight requests at once is not " +
		"the one most benchmarks print. Each session gets slower and the " +
		"server as a whole gets faster, and both of those are the same fact " +
		"seen from two ends. During decoding the engine reads the entire set " +
		"of weights to produce one token. If eight slots are busy it reads " +
		"that same set once and produces eight tokens from it, one for each " +
		"sequence, because the matrix multiply is batched across them. The " +
		"expensive part is amortised eight ways. What is not amortised is " +
		"the attention step, where every sequence has its own cache to read, " +
		"and the extra arithmetic that a wider batch demands. So the " +
		"aggregate climbs steeply at first and then flattens, while each " +
		"individual session decays gently. Around eight concurrent streams a " +
		"seventy billion parameter model on two consumer cards typically " +
		"gives you roughly half the single session speed per session and " +
		"about four times the total throughput. Which of those two numbers " +
		"matters depends entirely on what you are building. A person waiting " +
		"for a reply feels only their own stream, and below about ten tokens " +
		"a second reading becomes uncomfortable. A fleet of agents grinding " +
		"through tool calls feels only the aggregate, and would happily take " +
		"twice the sessions at half the speed. Quoting one figure when your " +
		"reader cares about the other is how benchmark arguments start. " +
		"There is a second effect worth knowing. Prompt processing does not " +
		"batch the same way, because each new request has its own prompt to " +
		"chew through before it can join the decode batch, and while a slot " +
		"is doing that it competes with the sessions already generating. " +
		"That is why time to first token spreads out as the slots fill, and " +
		"why a burst of arrivals makes ongoing streams stutter for a moment. " +
		"Watch the per stream rates side by side rather than one average, " +
		"because an average hides a slot that fell behind the others and " +
		"that is precisely the failure you want to catch early.",

	// 3 — the prefix cache.
	"Prefix caching is exact, and the word exact is doing a lot of work in " +
		"that sentence. The server keeps the key and value tensors it " +
		"already computed for a slot, and when the next request arrives it " +
		"compares the new token sequence with the old one from the " +
		"beginning. Every position that matches is reused. The first " +
		"position that differs ends the reuse, and everything after it is " +
		"evaluated again, however small the difference was. One extra space " +
		"in your system message, a timestamp you helpfully injected, a tool " +
		"list whose order comes out of a map and therefore changes between " +
		"runs, and the whole prefix is gone. This is why a chat that felt " +
		"instant yesterday can take a second to start today with no change " +
		"you would describe as a change. It is also why the hit figure is " +
		"printed next to every rate here. A prefill number without its cache " +
		"hit is not a measurement of anything, because you cannot tell " +
		"whether the server evaluated five hundred tokens or fifty. Two runs " +
		"of the same prompt on the same machine can differ by an order of " +
		"magnitude on that line alone, and people post both of them as " +
		"though they were comparable. Building for it is straightforward " +
		"once you accept the constraint. Put everything stable at the front: " +
		"the system prompt, the tool definitions, the few shot examples, " +
		"anything that does not depend on the request. Put the volatile " +
		"parts at the end, where invalidating them costs only their own " +
		"tokens. Serialise your tool list deterministically rather than " +
		"letting a hash map decide the order for you. If several agents " +
		"share a preamble, send them to the same server so they share the " +
		"slot that holds it. And when you cannot avoid a change, make it " +
		"late rather than early, because the cost is not the size of the " +
		"edit but the amount of prompt that sits behind it. The slot count " +
		"matters here too: caches live per slot, and a request routed to a " +
		"cold slot pays the full price even though a warm copy exists.",

	// 4 — 측정 위생 (Korean).
	"숫자를 재기 전에 기계가 조용한지부터 확인하는 습관이 결국 시간을 " +
		"아껴 준다. 다른 프로세스가 같은 카드를 쓰고 있으면 재는 대상보다 " +
		"잡음이 크고, 그런 상태에서 나온 값으로 설정을 바꾸면 엉뚱한 손잡이 " +
		"만 돌리게 된다. 그래서 이 화면은 로드 평균과 다른 연산 프로세스 " +
		"수를 항상 같이 보여 준다. 둘 중 하나라도 켜져 있으면 그 실행은 " +
		"비교 대상에서 빼는 편이 낫다. 두 번째로 볼 것은 첫 실행인지 " +
		"여부다. 모델 파일을 처음 읽는 순간에는 페이지 캐시가 비어 있어서 " +
		"디스크에서 올라오고, 그 비용이 토큰 시간에 섞여 들어간다. 토큰당 " +
		"메이저 폴트 수가 그 신호다. 이 값이 일 근처로 올라가 있으면 " +
		"가중치가 디코드 도중에 디스크에서 들어오고 있다는 뜻이고, 그때 " +
		"측정한 속도는 모델의 속도가 아니라 저장 장치의 속도다. 같은 " +
		"명령을 한 번 더 돌려서 숫자가 크게 뛰면 첫 실행이 차가웠던 " +
		"것이다. 세 번째는 온도와 전력이다. 소비자용 카드는 오래 돌리면 " +
		"클럭을 내린다. 삼십 초짜리 측정에서는 보이지 않다가 십 분짜리 " +
		"작업에서 십 몇 퍼센트가 사라지는 식이라, 짧은 벤치마크만 보고 " +
		"고른 설정이 실제 사용에서는 다르게 동작한다. 클럭이 내려가는 " +
		"지점이 찍혀 있으면 그 실행은 두 구간으로 나눠서 읽어야 한다. " +
		"네 번째는 프롬프트다. 길이와 캐시 적중률을 같이 적지 않은 프리필 " +
		"수치는 아무 뜻이 없다. 같은 서버에서 같은 모델로 잰 값이 두 배씩 " +
		"차이 나는 이유가 대부분 여기에 있다. 마지막으로 한 번만 재지 " +
		"말라. 같은 조건으로 세 번 돌려서 가운데 값을 쓰고, 세 값이 서로 " +
		"많이 다르면 그 사실 자체를 기록하라. 흔들리는 측정은 평균을 낼 " +
		"대상이 아니라 원인을 찾아야 할 증상이다. 이 도구가 실행마다 " +
		"기계 상태를 같이 저장하는 이유도 그것이고, 나중에 두 실행을 " +
		"비교할 때 어느 쪽을 믿어야 하는지 판단할 근거가 거기에 남는다. " +
		"설정을 바꾼 뒤에는 바꾼 항목 하나만 바꾸고 다시 재라. 두 개를 " +
		"동시에 돌리면 어느 쪽이 효과를 냈는지 영원히 알 수 없다. " +
		"기록하는 방식도 결과를 바꾼다. 속도만 적어 둔 표는 두 주 뒤에 " +
		"보면 아무 쓸모가 없다. 어떤 파일을 썼는지, 어떤 깃발을 줬는지, " +
		"프롬프트가 몇 토큰이었고 그중 몇 개가 캐시에서 왔는지, 그리고 " +
		"그때 기계가 무엇을 하고 있었는지가 같이 있어야 나중에 비교가 " +
		"된다. 같은 모델 이름이라도 파일마다 평균 비트 수가 다르고, " +
		"같은 깃발이라도 빌드가 다르면 동작이 다르다. 그래서 빌드 " +
		"번호까지 적어 두는 편이 좋다. 남에게 숫자를 보여 줄 때도 " +
		"마찬가지다. 조건이 빠진 수치는 토론을 만들지 못하고 말싸움만 " +
		"만든다. 상대가 같은 조건을 재현할 수 있을 만큼 적어 두면, " +
		"의견이 갈리더라도 어디서 갈렸는지가 드러난다. 마지막으로 " +
		"자기 결과를 의심하는 습관을 권한다. 기대와 정확히 맞는 숫자가 " +
		"나왔을 때가 가장 위험하다. 그럴 때는 일부러 반대 방향으로 " +
		"설정을 한 번 바꿔서 숫자가 예상대로 나빠지는지 확인해 보라. " +
		"계측기가 고장 나 있으면 그 위에 쌓은 판단이 전부 오염된다.",

	// 5 — quantisation.
	"A quantisation name is not a single number, and reading it as one is " +
		"the source of half the confusion about quality. In the common " +
		"mixed schemes the file is built tensor by tensor, and different " +
		"tensors get different treatment. The attention projections and the " +
		"feed forward matrices that dominate the file size take the low bit " +
		"width the name advertises. The embedding table and the output head " +
		"are usually kept at a higher width, because an error there lands on " +
		"every token rather than being averaged away inside a layer. That is " +
		"why two files with the same label can differ in size by a " +
		"gigabyte, and why the average bits per weight printed in the " +
		"loading log is the honest figure to compare rather than the name. " +
		"The practical ordering is well established by now. Four bit " +
		"medium mixes are where most people live: the loss against the full " +
		"precision model is small enough that you have to look for it with a " +
		"careful evaluation rather than a chat. Below four bits the loss " +
		"grows quickly and unevenly, hitting reasoning and long context " +
		"behaviour before it touches fluency, so a model can sound fine and " +
		"quietly get worse at the thing you actually wanted. Above four bits " +
		"the gains are real but small, and each step costs memory you could " +
		"have spent on context or on a larger model. A larger model at four " +
		"bits generally beats a smaller one at eight, and that trade is the " +
		"one worth making first. Two details catch people out. The first is " +
		"that quantisation does not change the arithmetic the device does: " +
		"the weights are unpacked on the fly and multiplied at a higher " +
		"precision, so the speed gain comes from moving fewer bytes rather " +
		"than from doing cheaper multiplications. The second is that the " +
		"importance matrix used while building the file matters. Two files " +
		"with identical names, one built with a calibration pass and one " +
		"without, will not behave identically, and nothing in the file name " +
		"tells you which you have downloaded.",

	// 6 — speculative decoding.
	"Speculative decoding is a bet, and it is worth knowing the odds before " +
		"you take it. A small draft model proposes several tokens, the large " +
		"model checks them all in a single forward pass, and every proposal " +
		"that matches what the large model would have produced is kept. The " +
		"output is identical to what you would have got without the draft, " +
		"which is the part that makes the technique respectable rather than " +
		"a quality trade. What you are trading is work. Each verification " +
		"pass costs about what a normal decoding step costs, because the " +
		"cost is dominated by reading the weights and checking four tokens " +
		"reads them exactly once. So if the draft is accurate you get " +
		"several tokens for the price of one. If it is not, you paid for the " +
		"draft passes and threw the proposals away. The break even point " +
		"depends on how expensive the draft is relative to the target. A " +
		"draft one tenth the size needs a fairly modest acceptance rate to " +
		"pay for itself, while a draft a quarter the size needs a good one. " +
		"Acceptance depends on the content more than on the models. Code, " +
		"structured output, repetitive formatting and quotation from the " +
		"prompt are easy to predict and accept at high rates. Open ended " +
		"prose with real choices in it accepts poorly. This is why the same " +
		"pair of models can double your speed on one workload and slow you " +
		"down on another, and why a single headline speedup figure tells you " +
		"almost nothing about your own case. Two practical notes. The draft " +
		"has to fit beside the target in memory, and on a rig that is " +
		"already close to full the memory it takes would often have bought " +
		"more context or a better quantisation instead. And batching " +
		"competes with speculation: when several streams are decoding " +
		"together the expensive pass is already amortised across them, so " +
		"the headroom speculation was exploiting is mostly gone. Measure it " +
		"with your own prompts, at the concurrency you actually run, and " +
		"keep the acceptance rate next to the speedup so the number can be " +
		"explained later.",

	// 7 — resident memory versus loaded weights.
	"Resident memory is not the same thing as loaded weights, and the gap " +
		"between them is where a whole genre of confused bug reports comes " +
		"from. The server maps the model file rather than reading it into " +
		"its own memory. A mapping costs address space, not pages. Pages " +
		"appear in the resident set when they are touched, and they can " +
		"leave again whenever the kernel needs the memory, because a clean " +
		"file backed page can be dropped and read back later. So the " +
		"resident figure tells you what this process has touched recently " +
		"and what the kernel has chosen to keep. It does not tell you what " +
		"the model needs, and subtracting it from the file size does not " +
		"give you the part that was never loaded. On a run with everything " +
		"offloaded the number is small and that is correct: the weights were " +
		"read once on the way to the device, and after that they live in " +
		"video memory. The page cache may still hold a copy, which is why a " +
		"second start is quick, but that copy belongs to the kernel and not " +
		"to the process. Virtual size stays enormous the whole time because " +
		"the mapping is still there. Neither number is wrong; they answer " +
		"different questions, and this screen prints both side by side for " +
		"exactly that reason. If you genuinely want to know which tensors " +
		"the engine never reads, the answer is in the file header rather " +
		"than in any process counter. The header lists every tensor with its " +
		"size and its place, so a reader that understands the placement " +
		"rules can add up what the current settings will actually touch. " +
		"That is where the never loaded figure on the card comes from, and " +
		"it is why the figure is absent rather than zero when a model is " +
		"fully offloaded. The counter that does matter during a run is the " +
		"major fault rate, because a major fault means a page had to come " +
		"back from storage while a token was waiting, and that is the one " +
		"memory event a reader can feel.",
}

// exampleThinking is what the thinking streams say before they answer, in the
// register a reasoning model actually uses: first person, clipped, planning
// rather than explaining. It is drawn dim, so a reader sees the model working
// without mistaking the monologue for the reply.
//
// The lengths differ on purpose. A stream that thinks briefly needs a few
// sentences; the stream that never stops thinking has to stay mid-thought for
// the whole run, so its monologue is as long as the answers are.
var exampleThinking = []string{
	// 0 — never used as a thinking stream; kept so the slices line up.
	"They are asking why a second card did not make one session faster. " +
		"Start from the bytes per token and let the arithmetic do the work.",

	// 1 — the brief thinker (Korean). Only the first couple of dozen words are
	// seen at most stream counts, but at two streams this is the monologue
	// that runs the whole budget, so it is as long as the others.
	"캐시 크기를 묻는 건지 품질을 묻는 건지 먼저 갈라야겠다. 계산식을 " +
		"주고 여덟 비트로 내렸을 때 줄어드는 양을 숫자로 보여 주는 쪽이 " +
		"빠르겠다. 그런데 질문을 다시 읽어 보면 둘이 섞여 있다. 자리가 " +
		"모자라서 방법을 찾는 중인데 품질이 걱정된다는 이야기에 가깝다. " +
		"그러면 순서는 정해진다. 먼저 얼마나 줄어드는지를 숫자로 보여 " +
		"주고, 그 절약이 무엇을 사 주는지 말한 다음, 품질 이야기는 " +
		"마지막에 짧게 붙이는 게 낫다. 계산식을 어디까지 풀어 쓸지 " +
		"고민이다. 레이어 수와 KV 헤드 채널 수를 직접 곱하게 하면 자기 " +
		"모델에 적용할 수 있어서 좋은데, 헤드 수를 어디서 읽는지 모르면 " +
		"거기서 막힌다. 로딩 로그에 캐시 크기가 한 줄로 찍히니 그걸 " +
		"먼저 알려 주고 식은 확인용으로 두자. 그 편이 실제로 쓰인다. " +
		"품질 쪽은 조심해야 한다. 여덟 비트 캐시가 가중치 네 비트와 " +
		"다르다는 건 말할 수 있지만, 얼마나 떨어지는지를 숫자로 " +
		"말하려면 근거가 필요하다. 내가 기억하는 수치는 모델과 작업에 " +
		"따라 편차가 커서 그대로 옮기면 오해를 만든다. 방향만 말하고 " +
		"직접 재 보라고 하는 편이 정직하다. 키가 값보다 민감하다는 " +
		"보고는 여러 곳에서 일관되게 나오니 이건 언급해도 되겠다. " +
		"값만 먼저 내려 보라는 조언이 실제로 쓸모가 있다. 플래시 " +
		"어텐션 이야기도 빠뜨리면 안 된다. 꺼 둔 상태에서 캐시만 " +
		"양자화하면 조합에 따라 아예 동작하지 않거나 느려진다. 이건 " +
		"질문자가 겪을 가능성이 높은 함정이고, 겪고 나면 캐시 양자화 " +
		"자체가 나쁘다고 결론 내릴 만한 종류의 함정이다. 순서를 다시 " +
		"정리하자. 로그에서 캐시 크기 읽는 법, 여덟 비트로 내렸을 때 " +
		"줄어드는 양, 그 자리로 무엇을 할 수 있는지, 플래시 어텐션 " +
		"전제, 그리고 품질은 방향만. 다섯 줄이면 충분하다. 길게 쓰면 " +
		"핵심인 첫 두 줄이 묻힌다. 한 가지 더 확인할 것은 슬롯 수다. " +
		"동시 요청을 여덟로 잡아 뒀다면 계산이 통째로 달라지는데, " +
		"질문에는 그 이야기가 없다. 물어볼지 가정할지 정해야 한다. " +
		"가정하면 틀렸을 때 조언 전체가 무의미해지니, 한 줄로 " +
		"확인하고 넘어가는 쪽이 낫겠다. 마지막으로 어투를 정하자. " +
		"이 사람은 이미 문서를 읽고 왔고 용어를 정확히 쓰고 있다. " +
		"기초부터 설명하면 시간 낭비로 느낄 것이다. 결론을 먼저 " +
		"놓고 근거를 뒤에 붙이는 구조가 맞겠다. 숫자를 줄 때는 " +
		"어디서 나온 숫자인지 한 단어라도 붙여 두자. 출처 없는 " +
		"수치는 다음 질문을 부르고, 그 질문에 답하려면 결국 " +
		"처음부터 다시 설명해야 한다. 그리고 내가 확신하지 못하는 " +
		"부분은 확신하지 못한다고 쓰자. 측정해 보라는 말이 무책임한 " +
		"회피처럼 들리지 않으려면, 무엇을 어떻게 재면 되는지까지 " +
		"같이 적어야 한다. 그 한 줄이 답변 전체의 신뢰를 " +
		"결정한다.",

	// 2 — thinks a while, then answers.
	"Two questions hiding in one here. They measured eight streams and saw " +
		"each one slower than the single stream run, and they want to know " +
		"whether something is broken. Nothing is broken, but saying that " +
		"first would sound dismissive. Better to give the mechanism: the " +
		"weights are read once per batched step, so the expensive part is " +
		"shared and the per session figure falls while the total rises. " +
		"Then the practical half, which is that the right number depends on " +
		"whether a person or an agent is waiting. I should also mention the " +
		"prefill interference, because their stutter complaint sounds like " +
		"arrivals landing on a busy server rather than a decoding problem. " +
		"Keep it short and lead with the mechanism. One thing to avoid: " +
		"quoting a scaling factor as though it were a constant. It depends " +
		"on the model, the context length and how full the batch is, and " +
		"they will hold me to whatever figure I give. Better to describe " +
		"the shape, steeply rising then flattening, and tell them to read " +
		"their own per stream rates side by side rather than one average, " +
		"since an average hides the slot that fell behind. What else " +
		"belongs in this? The memory side, probably. Every additional slot " +
		"carries its own key and value cache, so eight of them at a long " +
		"context is a serious amount of memory, and someone who raises the " +
		"parallel count without lowering the context per slot will meet an " +
		"allocation failure rather than a slowdown. That is worth a " +
		"sentence even though they did not ask, because it is the next " +
		"wall they will hit. Then there is the question of what the right " +
		"number of slots actually is. More slots than the workload has " +
		"sessions is pure waste: the memory is reserved whether or not " +
		"anything uses it. Fewer slots than sessions means requests queue, " +
		"which shows up as time to first token rather than as a lower rate, " +
		"and people rarely look there. Matching the slot count to the " +
		"concurrency they actually run is the boring answer and usually the " +
		"correct one. Should I bring up continuous batching explicitly? " +
		"The mechanism matters here: a slot that finishes leaves the batch " +
		"and the others speed up immediately, which is why the per stream " +
		"rate is not a constant across a run and why an average over the " +
		"whole run understates what a session felt at the start. If they " +
		"are measuring by wall clock across a mixed set of requests, that " +
		"alone could explain the numbers they are unhappy with. Worth " +
		"asking how they measured before I explain anything further. " +
		"Ordering, then: the mechanism in two sentences, the two numbers " +
		"and who each one belongs to, the prefill interference that " +
		"explains their stutter, and a question about how they measured. " +
		"Leave the memory note for the end where it will not derail the " +
		"main answer.",

	// 3 — the long monologue, for the stream that never reaches an answer.
	"Prefix caching question. Before I write anything I should work out " +
		"which failure they actually hit, because the symptom they describe " +
		"is consistent with three different causes and the advice differs " +
		"for each. First possibility: their prompt genuinely changes at the " +
		"front. A timestamp or a session identifier placed in the system " +
		"message would do it, and that is the most common mistake by a wide " +
		"margin. Second possibility: the prompt is stable but the " +
		"serialisation is not. Tool definitions coming out of a hash map " +
		"land in a different order between processes, and nothing in their " +
		"own code looks like a change even though the bytes differ. Third " +
		"possibility: the prompt and the serialisation are both stable but " +
		"the request went to a different slot, so the cache that holds the " +
		"prefix belongs to another sequence and this one starts cold. That " +
		"third case is the nastiest because it comes and goes with load, " +
		"which makes it look like a performance problem rather than a " +
		"routing one. How would I tell them apart from the outside? The hit " +
		"count is the first thing to read: zero on every request points at " +
		"the prompt itself, while an intermittent zero points at routing. " +
		"If they can log the rendered prompt and hash it, comparing two " +
		"consecutive hashes settles the first two cases immediately. That " +
		"is probably the advice worth leading with, since it turns an " +
		"argument into a measurement. Then the ordering principle, stable " +
		"content first and volatile content last, which fixes the first two " +
		"cases and mitigates the third. I should be careful not to promise " +
		"that reuse survives everything. There are builds where a change to " +
		"the sampling parameters does not invalidate anything and builds " +
		"where slot management differs, and quoting a rule that does not " +
		"hold on their version would be worse than saying nothing. Better " +
		"to describe what is certainly true everywhere: matching happens " +
		"from the beginning, the first difference ends it, and what follows " +
		"is paid for again. Also worth a sentence: the cost is not the size " +
		"of the edit, it is the amount of prompt sitting behind the edit. " +
		"People hear that a one character change costs everything and " +
		"assume the tool is fragile, when the rule is mechanical and easy " +
		"to design around once stated plainly. Do I need to explain why " +
		"reuse has to be exact? A brief reason helps: the cached tensors " +
		"are functions of every earlier token, so a single different token " +
		"upstream changes all of them, and there is no partial repair short " +
		"of recomputing. One sentence on that, not a lecture. Order of the " +
		"reply, then. Mechanism, the measurement that identifies their " +
		"case, the layout rule, and the slot caveat at the end where it " +
		"will not distract from the part they can act on today.",

	// 4 — the long monologue in Korean.
	"측정이 흔들린다고 했으니 기계가 조용했는지부터 물어보는 게 맞다. " +
		"다만 순서를 잘 잡아야 한다. 원인 후보가 여럿인데 확인 비용이 " +
		"제각각이라, 싸고 결정적인 것부터 세워야 상대가 실제로 따라온다. " +
		"가장 싼 확인은 같은 명령을 한 번 더 돌려 보는 것이다. 두 번째 " +
		"실행이 크게 빨라지면 첫 실행이 차가웠던 것이고, 그러면 페이지 " +
		"캐시 이야기만 하면 끝난다. 여기서 토큰당 메이저 폴트 값을 같이 " +
		"보게 하면 추측이 측정으로 바뀐다. 그 다음이 다른 프로세스다. " +
		"로드 평균과 다른 연산 프로세스 수를 같이 보라고 하면 되는데, " +
		"화면에 이미 둘 다 있으니 어디를 보라고만 말하면 된다. 여기까지가 " +
		"환경 문제고, 통과하면 설정 문제로 넘어간다. 설정 쪽에서 먼저 " +
		"의심할 것은 프롬프트다. 길이와 캐시 적중률을 적지 않은 프리필 " +
		"수치는 비교가 안 된다는 점을 분명히 해야 한다. 이 사람은 아마 " +
		"두 실행의 프롬프트가 달랐다는 사실을 모르고 있을 가능성이 높다. " +
		"그 다음이 온도와 클럭인데, 이건 짧은 측정에서는 잘 안 보이니 " +
		"측정 길이를 물어봐야 판단이 선다. 삼십 초짜리였다면 아직 " +
		"떨어지기 전이고, 십 분짜리였다면 거의 확실히 내려갔다. 어느 쪽을 " +
		"먼저 쓸지 고민되는데, 상대가 이미 여러 번 재 봤다고 했으니 첫 " +
		"실행 문제는 아닐 수도 있다. 그래도 순서를 바꾸지는 말자. 확인이 " +
		"싸고 결과가 확실한 것부터 가는 편이 결국 빠르다. 한 가지 더 " +
		"짚을 것은 변수 하나씩 바꾸라는 원칙이다. 지금 설명만 하면 다음 " +
		"라운드에 두 개를 같이 바꾸고 다시 물어볼 게 뻔하다. 마지막으로 " +
		"세 번 돌려 가운데 값을 쓰라는 이야기를 넣을지 말지. 넣는 편이 " +
		"낫겠다. 다만 평균을 내라고 하면 안 된다. 값이 흔들린다는 사실 " +
		"자체가 증상이고, 평균은 그 증상을 지워 버린다. 정리하면 순서는 " +
		"이렇다. 다시 한 번 돌려 보기, 폴트 값 확인, 다른 프로세스 확인, " +
		"프롬프트와 적중률 기록, 측정 길이와 클럭, 그리고 한 번에 하나만 " +
		"바꾸기. 말투는 담담하게 가고, 도구 자랑처럼 읽히지 않게 화면 " +
		"이야기는 한 줄로 줄이자. 다시 보니 빠뜨린 게 하나 있다. 서버를 " +
		"언제 띄웠는지도 물어봐야 한다. 모델을 올린 직후에는 캐시가 " +
		"비어 있을 뿐 아니라 장치 클럭도 아직 낮은 상태에서 올라오는 " +
		"중이라, 첫 요청 몇 개는 어느 쪽이든 느리게 나온다. 이건 폴트 " +
		"값만으로는 안 갈린다. 그리고 슬롯 이야기도 한 줄 필요할 것 " +
		"같다. 동시 요청을 여러 개 보내 놓고 개별 속도가 느리다고 하는 " +
		"경우가 꽤 많은데, 그건 잘못된 측정이 아니라 다른 질문이다. " +
		"상대가 어느 쪽을 재고 싶은지부터 정해야 조언이 갈린다. 사람이 " +
		"기다리는 화면이면 스트림 하나의 속도가 답이고, 에이전트를 " +
		"여럿 돌리는 거라면 합계가 답이다. 순서를 다시 정리하면, 환경 " +
		"확인이 먼저고 그다음이 무엇을 재려는지 확정하는 것이다. 사실 " +
		"후자가 먼저일지도 모르겠다. 재려는 대상이 정해지지 않은 상태에서 " +
		"환경만 깨끗하게 만들어 봐야 나온 숫자를 해석할 기준이 없다. " +
		"그래도 상대는 이미 숫자를 들고 왔으니, 그 숫자를 어떻게 읽어야 " +
		"하는지부터 말하는 편이 자연스럽겠다. 길게 쓰지는 말자. 항목이 " +
		"여섯 개가 넘어가면 아무도 끝까지 하지 않는다. 세 개로 줄이고 " +
		"나머지는 필요하면 다시 묻게 두는 쪽이 실제로 더 많이 실행된다.",

	// 5 — the long monologue on quantisation.
	"Quantisation question, and the way they phrased it tells me they " +
		"think the name is a single precision applied to the whole file. " +
		"That is the misunderstanding to fix first, because every other " +
		"confusion in their message follows from it. If the name were a " +
		"single number then two files with the same name would be the same " +
		"size, and they are not, and that discrepancy is what prompted the " +
		"question. So: mixed schemes, tensor by tensor, with the parts " +
		"whose errors propagate furthest kept wider. Say which parts those " +
		"are, since it makes the rule memorable rather than arbitrary. " +
		"Should I give exact bit widths per tensor type? Tempting, but they " +
		"vary between builds and between the people producing the files, " +
		"and a table that is wrong on their particular download is worse " +
		"than a principle that is right everywhere. Point them at the " +
		"average bits per weight in the loading log instead. It is printed, " +
		"it is exact, and it is comparable across files in a way the names " +
		"are not. Next, the quality ordering. I want to be careful here " +
		"because it is easy to state opinion as measurement. What is " +
		"reasonably well supported: the loss is small and hard to notice at " +
		"four bit medium mixes, it grows quickly below that, and it lands " +
		"on reasoning and long context before it lands on fluency. That " +
		"last point is the useful one, since it explains why a model can " +
		"seem fine in a chat and fail the task they actually cared about. " +
		"Then the trade that matters most in practice, which is a bigger " +
		"model at four bits against a smaller one at eight. I should give " +
		"the direction without pretending it holds in every case. The speed " +
		"question is worth pre empting too. They will assume a smaller file " +
		"means cheaper arithmetic. It does not: the weights are unpacked " +
		"and multiplied at a higher precision anyway, and the win comes " +
		"from moving fewer bytes. Getting that straight also explains why " +
		"the effect on generation is large and the effect on prompt " +
		"processing is small, which is the next thing they will notice. " +
		"Finally the calibration pass. Two files, same name, different " +
		"behaviour, and no way to tell from the file name alone. Worth one " +
		"sentence so they know to check the source rather than assume. " +
		"Order: what the name really means, how to read the real figure, " +
		"the quality ordering, the size trade, why speed moves, and the " +
		"calibration caveat last.",

	// 6 — the monologue that never reaches an answer.
	"Speculative decoding, and they want to know whether it is worth " +
		"turning on. I need the break even point before I can answer that " +
		"properly, so let me work it rather than quoting the number I half " +
		"remember. Each verification pass costs roughly one ordinary " +
		"decoding step, because the cost is reading the weights and a batch " +
		"of four candidate tokens reads them once. The draft costs whatever " +
		"its own forward passes cost, one per proposed token. So for a " +
		"draft that is a tenth the size, proposing four tokens costs about " +
		"four tenths of a step plus the one step of verification, call it " +
		"one point four steps, and it returns however many tokens were " +
		"accepted. Break even is where accepted tokens exceed that cost, so " +
		"somewhere near one and a half tokens per round, which is an " +
		"acceptance rate of under forty per cent for a four token proposal. " +
		"That is comfortably reachable on structured output. Now a draft a " +
		"quarter of the size: one step of verification plus four quarters " +
		"of a step of drafting, so two steps, and it needs better than two " +
		"accepted tokens per round to pay for itself. That is around fifty " +
		"five per cent acceptance, which open ended prose will often miss. " +
		"Good, the two cases separate cleanly and that is the shape of the " +
		"answer. Now, does the arithmetic hold under batching? I do not " +
		"think it does, and this is the part I would get wrong if I " +
		"answered quickly. With eight streams already decoding together the " +
		"weight read is shared across all of them, so the marginal cost of " +
		"a token is no longer dominated by memory, and the spare capacity " +
		"speculation was exploiting has already been spent on the other " +
		"sequences. The technique wants an idle machine with one stream on " +
		"it. Their message says agents, which means concurrency, which " +
		"means my whole break even calculation is answering a question they " +
		"are not in. I should lead with that rather than bury it. Let me " +
		"also check the memory side before I commit to advice. The draft " +
		"has to be resident alongside the target, and on two consumer cards " +
		"that are already nearly full, the space it takes is space that " +
		"would otherwise hold context or a better quantisation of the main " +
		"model. So the honest recommendation has three branches: single " +
		"stream and structured output, probably yes; single stream and " +
		"prose, measure first; concurrent workload, almost certainly spend " +
		"the memory elsewhere. Is there anything that would change the " +
		"middle branch? Acceptance rates vary so much between prompt kinds " +
		"that I should not guess on their behalf, and the measurement is " +
		"cheap enough that recommending it is not a dodge. One more thing " +
		"to verify before I write: whether their server reports the draft " +
		"acceptance count, because advice to measure is useless if the " +
		"number is not exposed. The timings object does carry a drafted " +
		"and an accepted count when speculation is on, so the measurement " +
		"is available and I can tell them where to look for it",

	// 7 — never used as a thinking stream; kept so the slices line up.
	"They are subtracting resident memory from the file size to work out " +
		"what was never loaded. That is the mistake. Say where the real " +
		"figure comes from before explaining why the subtraction fails.",
}
