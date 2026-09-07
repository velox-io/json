1. 流式解析本质上值支持多个非连续buffer的解析；
2. 非连续buffer可能在数据任何位置阶段，因此，我们一定要做一个不变量: 每个buffer解析完成之后，不会在primitive(string, number, bool, null)中间截断;
3. 第二点意味着buffer末尾可能要拷贝一点点内容到下一个buffer开头进行拼接；
4. EOF要分三态: 当一个buffer遇到EOF后，可能不是真的EOF，而是需要下一段数据进buffer；EOF的处理可以在Go实现，这样我估计对非stream数据的性能没什么影响；
5. buffer末尾的余量拷贝可能可以借助simd 索引快速找到它的位置；
6. 每个buffer其实正常地进行padding后进行扫描即可，现有主体架构应该不用大改；
7. 上层stream处理模式/类型可以先搁置
